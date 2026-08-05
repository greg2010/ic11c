package devtrace

import (
	"fmt"
	"math"
	"strconv"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
)

// formatWrite renders one write the way a failure is read. Property is read
// against [ic10.LogicType] or [ic10.LogicSlotType] depending on Slot.
func formatWrite(w chip.Write) string {
	if w.Slot == chip.NoSlot {
		return fmt.Sprintf("d%d %s = %s", w.Pin, logicTypeName(w.Property), formatValue(w.Value))
	}
	return fmt.Sprintf("d%d slot %d %s = %s", w.Pin, w.Slot, slotTypeName(w.Property), formatValue(w.Value))
}

// sameWrite reports whether two writes are the same write, comparing their
// values with equal.
func sameWrite(a, b chip.Write, equal valueRule) bool {
	return a.Pin == b.Pin && a.Property == b.Property && a.Slot == b.Slot && equal(a.Value, b.Value)
}

// valueRule is how far two runs' written values are held to each other. Which
// one applies is [Diff]'s to decide, from what produced each trace.
type valueRule func(a, b float64) bool

// sameBits holds two values to their bit patterns rather than to their
// numbers, since NaN is not equal to itself and would otherwise report two
// runs that both wrote NaN as disagreeing. Bit patterns also keep the two
// zeroes apart, which the chip's own division does distinguish.
func sameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

// sameBitsOrBothNaN is [sameBits] with the payload of a NaN left out. It is
// the rule between two machines: a NaN's payload is each machine's own
// convention, but the two zeroes, the two infinities and a NaN against a
// number are still distinct.
func sameBitsOrBothNaN(a, b float64) bool {
	return sameBits(a, b) || (math.IsNaN(a) && math.IsNaN(b))
}

// producer is what ran a program to make a trace. Two traces from one
// producer came off one machine, and [Diff] holds those to more than it can
// hold two machines to.
type producer uint8

const (
	// producerUnset is a [Trace] nothing here made. It is the zero value so
	// that an unstamped trace fails [Trace.stamped] instead of silently being
	// read as a chip trace.
	producerUnset producer = iota
	// producerChip is emitted assembly on the game's own chip, which is [Run].
	producerChip
	// producerNative is a C compiler's build of the same source running on the
	// host, which is [RunNative].
	producerNative
)

var producerNames = [...]string{
	producerUnset:  "no producer",
	producerChip:   "chip",
	producerNative: "native",
}

func (p producer) String() string { return source.EnumName(producerNames[:], int(p), "producer") }

// StopReason is why a run ended.
type StopReason uint8

const (
	// stopUnset is a run whose ending nobody observed. It is the zero value so
	// that a [Stop] nothing set is not read as a run that used up every
	// segment it was given.
	stopUnset StopReason = iota
	// StopSegments means the run used up the segments it was given while the
	// program was still going. It is how a control loop ends a run.
	StopSegments
	// StopEnded means the program counter left the program, which is how a
	// MicroC main returns.
	StopEnded
	// StopFaulted means the chip raised an exception and the run stopped there.
	StopFaulted
	// StopBudget means a segment spent its whole instruction budget without
	// reaching a yield, so two builds are no longer aligned and the run is not
	// comparable.
	StopBudget
)

var stopReasonNames = [...]string{
	stopUnset:    "nothing observed how the run ended",
	StopSegments: "ran every segment",
	StopEnded:    "the program ended",
	StopFaulted:  "the chip faulted",
	StopBudget:   "a segment reached no yield",
}

func (r StopReason) String() string {
	return source.EnumName(stopReasonNames[:], int(r), "StopReason")
}

// Stop is how a run ended.
type Stop struct {
	Reason StopReason
	// Fault is the exception a StopFaulted run raised.
	Fault chip.ExceptionType
	// Line is where that fault was raised. It is reported and never compared:
	// two builds of one program lay their instructions out differently, so the
	// same fault reaches a caller from a different line in each.
	Line int
}

func (s Stop) String() string {
	if s.Reason != StopFaulted {
		return s.Reason.String()
	}
	return fmt.Sprintf("%s: %s at line %d", s.Reason, s.Fault, s.Line)
}

// Trace is one run as a device could tell it.
type Trace struct {
	// Name says which run this is, so a difference names the two builds rather
	// than calling them left and right.
	Name string
	// Events is every write the run made, in order.
	Events []chip.Write
	// Segments is how many turns of the control loop ran. The segment a run
	// stopped in counts, however it stopped: [Run] and [RunNative] both hold to
	// that, so two runs stopping at the same point in the source report the
	// same count.
	Segments int
	Stop     Stop
	// producer is what ran the program. It is set by whichever of [Run] and
	// [RunNative] made the trace and is not a caller's to choose, so it is not
	// published; [Diff] is the only thing that reads it.
	producer producer
}

// stamped reports whether a trace carries the two things a run observes rather
// than defaults to. [Run] and [RunNative] set both, so only a trace assembled
// by hand can fail it.
func (t Trace) stamped() error {
	switch {
	case t.producer == producerUnset:
		return fmt.Errorf("%s says nothing about what ran it, so how far it can be held to another trace is undecided", t.Name)
	case t.Stop.Reason == stopUnset:
		return fmt.Errorf("%s says nothing about how its run ended, so there is no ending to compare", t.Name)
	}
	return nil
}

// Difference is one way two traces disagree, which is what [Diff] returns.
type Difference struct {
	// OnAWrite means both runs made the write at that point in their sequences
	// and wrote different things. Anything else is one run going further.
	OnAWrite bool
	report   string
}

func (d *Difference) Error() string { return d.report }

// Diff reports the first way two traces disagree, or nil when the two runs are
// indistinguishable from outside the chip.
//
// Two traces from one producer are compared bit for bit; two from different
// producers are compared bit for bit except that two NaNs count as equal,
// since a NaN's payload is each machine's own convention. The faulting line is
// deliberately not compared; see [Stop]. A trace that never said what produced
// it or how its run ended is refused with a plain error rather than compared.
func Diff(a, b Trace) error {
	if err := a.stamped(); err != nil {
		return err
	}
	if err := b.stamped(); err != nil {
		return err
	}
	var equal valueRule = sameBitsOrBothNaN
	if a.producer == b.producer {
		equal = sameBits
	}
	for i := range min(len(a.Events), len(b.Events)) {
		if !sameWrite(a.Events[i], b.Events[i], equal) {
			return &Difference{OnAWrite: true, report: fmt.Sprintf(
				"write %d differs: %s wrote %s where %s wrote %s",
				i, a.Name, formatWrite(a.Events[i]), b.Name, formatWrite(b.Events[i]))}
		}
	}
	if len(a.Events) != len(b.Events) {
		return &Difference{report: fmt.Sprintf(
			"%s made %d writes and %s made %d, agreeing on all %d they share; the first unmatched write is %s",
			a.Name, len(a.Events), b.Name, len(b.Events), min(len(a.Events), len(b.Events)),
			unmatched(a, b))}
	}
	if a.Segments != b.Segments {
		return &Difference{report: fmt.Sprintf(
			"%s ran %d segments and %s ran %d, so one control loop turned more often than the other",
			a.Name, a.Segments, b.Name, b.Segments)}
	}
	if a.Stop.Reason != b.Stop.Reason || (a.Stop.Reason == StopFaulted && a.Stop.Fault != b.Stop.Fault) {
		return &Difference{report: fmt.Sprintf(
			"%s ended because %s and %s ended because %s", a.Name, a.Stop, b.Name, b.Stop)}
	}
	return nil
}

// unmatched renders the first write the shorter trace does not have.
func unmatched(a, b Trace) string {
	longer, shorter := a, b
	if len(b.Events) > len(a.Events) {
		longer, shorter = b, a
	}
	return fmt.Sprintf("%s from %s", formatWrite(longer.Events[len(shorter.Events)]), longer.Name)
}

// formatValue renders a written value the way the chip's own number syntax
// would, with no exponent. A NaN renders as its bit pattern instead, since
// every NaN spells itself "NaN" otherwise and a difference between two would
// report a value against itself.
func formatValue(value float64) string {
	if math.IsNaN(value) {
		return fmt.Sprintf("NaN(0x%016x)", math.Float64bits(value))
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// logicTypeName and slotTypeName render a property ordinal. A property the
// generated tables do not name is rendered as its ordinal rather than dropped,
// since an unnamed property in a trace is a finding.
func logicTypeName(property int) string {
	if name, ok := logicTypeNames[ic10.LogicType(property)]; ok {
		return name
	}
	return "LogicType(" + strconv.Itoa(property) + ")"
}

func slotTypeName(property int) string {
	if name, ok := slotTypeNames[ic10.LogicSlotType(property)]; ok {
		return name
	}
	return "LogicSlotType(" + strconv.Itoa(property) + ")"
}

// logicTypeNames and slotTypeNames are the reverse of the generated name
// indexes. Several names can share one ordinal, since a renamed property
// keeps its old spelling working; the first name the table gives an ordinal is
// the one used.
var (
	logicTypeNames = reverseIndex(ic10.LogicTypes,
		func(t ic10.LogicTypeInfo) (ic10.LogicType, string) { return t.Value, t.Name })
	slotTypeNames = reverseIndex(ic10.LogicSlotTypes,
		func(t ic10.LogicSlotTypeInfo) (ic10.LogicSlotType, string) { return t.Value, t.Name })
)

func reverseIndex[E any, K comparable](entries []E, split func(E) (K, string)) map[K]string {
	index := make(map[K]string, len(entries))
	for _, entry := range entries {
		key, name := split(entry)
		if _, taken := index[key]; !taken {
			index[key] = name
		}
	}
	return index
}
