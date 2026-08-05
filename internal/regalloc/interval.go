package regalloc

import (
	"slices"

	"github.com/greg2010/ic11c/internal/mir"
)

// Program points. Each instruction owns two: an even point where it reads its
// operands and an odd point where it writes its result. Separating them lets a
// result take the register of an operand that dies in the same instruction.
func usePoint(index int) int { return 2 * index }
func defPoint(index int) int { return 2*index + 1 }

// liveRange is half open: to is the first point at which the value is dead.
type liveRange struct {
	from int
	to   int
}

// interval is the set of points at which a virtual register holds a value that
// is still wanted, kept as a list of ranges rather than a single span: filling
// the hole between two live blocks would make the value interfere with
// everything defined between them, which spills it needlessly.
type interval struct {
	vreg mir.VirtReg
	// ranges is sorted by from, disjoint, and never adjacent: construction
	// merges a range that touches its neighbour.
	ranges []liveRange
	// touches counts the operand positions naming vreg across the function,
	// definitions and uses alike. It is the number of extra instructions
	// spilling this interval would emit.
	touches int
}

// start and end are the first and last points of the interval, which
// [buildIntervals] guarantees covers at least one point. An empty interval
// reaching [spillScore] would divide zero touches by a zero span, and the
// resulting NaN wins every comparison, taking a register off a live value
// silently.
func (iv *interval) start() int { return iv.ranges[0].from }

func (iv *interval) end() int { return iv.ranges[len(iv.ranges)-1].to }

// span is the number of points at which the interval is live, excluding its
// holes. It is what a register gains by not holding this value.
func (iv *interval) span() int {
	n := 0
	for _, r := range iv.ranges {
		n += r.to - r.from
	}
	return n
}

func (iv *interval) covers(point int) bool {
	for _, r := range iv.ranges {
		if point < r.from {
			return false
		}
		if point < r.to {
			return true
		}
	}
	return false
}

func (iv *interval) intersects(other *interval) bool {
	i, j := 0, 0
	for i < len(iv.ranges) && j < len(other.ranges) {
		a, b := iv.ranges[i], other.ranges[j]
		if a.from < b.to && b.from < a.to {
			return true
		}
		if a.to <= b.to {
			i++
		} else {
			j++
		}
	}
	return false
}

// addRange extends the interval to cover [from, to). Construction walks blocks
// and instructions backwards, so from never exceeds the start of the first
// range already recorded: the new range either merges with that one or is
// inserted ahead of it, and a gap between the two is a hole worth keeping.
func (iv *interval) addRange(from, to int) {
	if to <= from {
		return
	}
	if len(iv.ranges) > 0 && to >= iv.ranges[0].from {
		iv.ranges[0].from = min(iv.ranges[0].from, from)
		iv.ranges[0].to = max(iv.ranges[0].to, to)
		return
	}
	iv.ranges = slices.Insert(iv.ranges, 0, liveRange{from: from, to: to})
}

// byStart orders intervals by their first live point, which is the order
// linear scan considers them in and the order spill slots are handed out in.
// The virtual register ID breaks ties and is a total order: intervals arrive
// from a map traversal, and slices.SortFunc is unstable, so without a
// tiebreaker map order would decide the emitted program.
func byStart(a, b *interval) int {
	if n := a.start() - b.start(); n != 0 {
		return n
	}
	return int(a.vreg.ID) - int(b.vreg.ID)
}

// setFrom shortens the first range to start at point, which is what a
// definition does to a value the block had assumed live from its start. A
// definition nothing reads is given its own range by [buildIntervals] instead,
// so there is always a range to shorten here.
func (iv *interval) setFrom(point int) {
	iv.ranges[0].from = point
}
