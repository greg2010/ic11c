package emit

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
)

// Limits the game's editor enforces on a program, from the constants of the
// same meaning on Assets.Scripts.UI.InputSourceCode. The chip's own compiler
// enforces none of them, so exceeding one produces a program the player cannot
// paste in rather than one that faults.
const (
	// MaxBytes is the program size budget. Reaching it before the line limit
	// takes an average line of 32 characters, which no measured program comes
	// near, though which one actually binds is computed per program by
	// Report.Binding rather than assumed.
	MaxBytes = 4096
	// MaxLines is the line count limit, and the budget most programs meet
	// first. It is also the per-tick instruction budget, though overrunning
	// that is not an error.
	MaxLines = 128
	// MaxLineLength is the character limit for one line. Emitted text is ASCII
	// only, so a byte count is a character count.
	MaxLineLength = 90
)

// MaxSlots is the chip's whole memory: one array shared by the data region and
// the call frames, with nothing between them.
//
// It is not one of the limits the editor enforces. A program over it assembles
// and pastes and then overwrites a global from a call frame with nothing
// trapping, which is why the report states what is left rather than only what
// was spent.
const MaxSlots = ic10.NumMemorySlots

// Report accounts for the size of an emitted program.
//
// Every line is charged len(line)+1 bytes, the line and the newline that ends
// it, so a function's bytes sum exactly to the program's. Text omits the final
// newline and is therefore one byte shorter than Bytes, which errs toward
// declaring a program over budget rather than under.
type Report struct {
	Bytes int
	Lines int
	// LongestLine is the character count of the widest emitted line, which is
	// what the 90 character limit is spent against.
	LongestLine int
	// Slots accounts for the memory array, which no emitted line reveals: the
	// layout is decided before emission and arrives in Options.
	Slots SlotReport
	// Functions is in emission order and covers every function, including ones
	// that emitted nothing.
	Functions []FuncReport
	// Sites attributes every byte to the construct that produced it, largest
	// first. It is the report's answer to what to cut.
	Sites []SiteReport
	// Violations lists every limit the program exceeds, in line order with
	// whole-program violations first. It is empty for a program that fits.
	Violations []Violation
}

// SlotReport accounts for the memory array a program was laid out against.
//
// It is both an input to Emit and part of its report: no emitted line names the
// boundary, so the numbers come from the stages that decided them. The zero
// value describes a program that allocates nothing and calls nothing.
type SlotReport struct {
	// Data counts the slots holding globals, arrays and address-taken locals,
	// which start at slot 0.
	Data int
	// Spill counts the slots register allocation spilled into, which start
	// above the data slots.
	Spill int
	// Frames reports whether a real call was selected. Only a call frame grows
	// into the slots above the two, so a program without one leaves the
	// headroom unspent rather than merely unused.
	Frames bool
}

// Used is the slots the data region occupies, which is also the first slot a
// call frame may take.
func (s SlotReport) Used() int { return s.Data + s.Spill }

// Headroom is the slots left for call frames to grow into.
//
// It is reported and not ranked against the text limits, and no figure for it
// is provably enough: frame depth is bounded statically only for a program with
// no recursion, and recursion is the only thing that forces a real call at all.
// What the number answers is how far a program is from the failure the array
// does not trap.
func (s SlotReport) Headroom() int { return MaxSlots - s.Used() }

// FuncReport attributes part of a program's size to one function.
type FuncReport struct {
	Name string
	// Pos is the function's source location, so a caller holding only the
	// report can point at the code that grew.
	Pos source.Position
	// Bytes and Lines cover the function's own lines only.
	Bytes int
	Lines int
	// FirstLine is the 0-based line the function starts at. For a function
	// that emitted nothing it is where it would have started.
	FirstLine int
}

// SiteReport attributes part of a program's size to one construct: an emitted
// function, or one call inlined into it.
//
// A function is the wrong unit on this target. Calls are inlined by default, so
// a function called from three places pays for itself three times and its total
// says nothing about which of the three calls to delete. Chain is what separates
// them: two expansions of one callee differ in the call they came through, not
// in the lines they were written on.
//
// Sites nest. Bytes covers everything spliced in below the site as well as the
// code at it, because that is what deleting the call would recover; Own is the
// code at the site alone, and it is Own that sums across every site to the
// program.
type SiteReport struct {
	// Func names the emitted function the lines landed in.
	Func string
	// Chain is the calls the code was spliced through, outermost first. It is
	// empty for the function itself.
	Chain []source.InlineSite
	// Bytes and Lines cover this site and everything inlined below it.
	Bytes int
	Lines int
	// Own is the bytes of the code at this site alone.
	Own int
}

// Depth is how many calls deep the site is, and is the indentation a rendered
// report uses to show that a site's bytes are counted in its parent's too.
func (s SiteReport) Depth() int { return len(s.Chain) }

// Label names the construct: the function for a top level site, and the
// innermost call for an inlined one. The calls above it are the enclosing rows.
func (s SiteReport) Label() string {
	if len(s.Chain) == 0 {
		return s.Func
	}
	return s.Chain[len(s.Chain)-1].String()
}

// ViolationKind names the limit a Violation reports.
type ViolationKind uint8

const (
	// ViolationBytes is the program size budget.
	ViolationBytes ViolationKind = iota
	// ViolationLines is the program line count.
	ViolationLines
	// ViolationLineLength is one line's character count.
	ViolationLineLength
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationBytes:
		return "bytes"
	case ViolationLines:
		return "lines"
	case ViolationLineLength:
		return "line length"
	default:
		return "ViolationKind(" + strconv.FormatUint(uint64(k), 10) + ")"
	}
}

// Noun names the limit as the subject of a sentence, which String's plural
// form is not.
func (k ViolationKind) Noun() string {
	switch k {
	case ViolationBytes:
		return "the byte budget"
	case ViolationLines:
		return "the line count"
	case ViolationLineLength:
		return "the line width"
	default:
		return k.String()
	}
}

// Violation is one target limit the emitted program exceeds. The program is
// emitted anyway, since knowing which constructs account for the size is more
// useful than a refusal, and the command reports it by failing.
type Violation struct {
	Kind ViolationKind
	// Line is the 0-based offending line, or -1 for a whole-program violation.
	Line int
	// Pos locates the instruction on that line, and is invalid for a
	// whole-program violation. Compiler-introduced code carries a position too:
	// a spill takes the position of the instruction it serves and the stack
	// pointer initialization takes the entry function's.
	Pos source.Position
	Msg string
}

// Spend is what a program spends against one of the target's limits.
type Spend struct {
	Kind ViolationKind
	Used int
	Max  int
}

// Percent is the share of the limit the program spends, rounded down. It can
// exceed 100.
func (s Spend) Percent() int {
	if s.Max == 0 {
		return 0
	}
	return s.Used * 100 / s.Max
}

// Spends lists what the program costs against each of the three limits, in the
// order the target documents them.
func (r Report) Spends() []Spend {
	return []Spend{
		{Kind: ViolationBytes, Used: r.Bytes, Max: MaxBytes},
		{Kind: ViolationLines, Used: r.Lines, Max: MaxLines},
		{Kind: ViolationLineLength, Used: r.LongestLine, Max: MaxLineLength},
	}
}

// Binding names the size limit the program spends the largest share of, which
// is the one that decides how much room is left.
//
// Only the two whole-program budgets are ranked. The 90 character line width
// bounds the formatting of one line and no program can outgrow it: a program at
// the width limit is one line too wide, not one line from being too big.
// Spending 60% of it says nothing about how much program is left, so it is
// reported and not ranked.
//
// Which of the two binds is computed rather than asserted. Lines bind on every
// program measured — a real instruction is far short of the 32 bytes the two
// caps would have to be balanced at — but a program of long lines can still
// meet 4096 bytes first, and telling that program to cut lines would be wrong.
// Ties go to whichever Spends lists first.
func (r Report) Binding() Spend {
	var binding Spend
	ranked := false
	for _, spend := range r.Spends() {
		if spend.Kind == ViolationLineLength {
			continue
		}
		// Cross-multiplied rather than divided: the shares are close enough on a
		// small program that rounding would decide the answer.
		if !ranked || spend.Used*binding.Max > binding.Used*spend.Max {
			binding, ranked = spend, true
		}
	}
	return binding
}

// String renders the report: one line for the limits, one naming the limit that
// binds, one for the memory array, one per violation, and the attribution of
// every byte to the construct that produced it.
func (r Report) String() string {
	spends := r.Spends()
	var b strings.Builder
	fmt.Fprintf(&b, "program: %d of %d bytes (%d%%), %d of %d lines (%d%%), longest line %d of %d characters (%d%%)",
		spends[0].Used, spends[0].Max, spends[0].Percent(),
		spends[1].Used, spends[1].Max, spends[1].Percent(),
		spends[2].Used, spends[2].Max, spends[2].Percent())
	binding := r.Binding()
	fmt.Fprintf(&b, "\n  closest limit: %s, at %d%% spent", binding.Kind.Noun(), binding.Percent())
	b.WriteString("\n  data region: " + r.Slots.describe())
	for _, violation := range r.Violations {
		b.WriteString("\n  over limit: ")
		b.WriteString(violation.Msg)
	}
	if len(r.Sites) > 0 {
		b.WriteString("\n  bytes by construct, largest first; an indented call's bytes are counted in the line above it:")
		for _, site := range r.Sites {
			share := 0
			if r.Bytes > 0 {
				share = site.Bytes * 100 / r.Bytes
			}
			fmt.Fprintf(&b, "\n    %5d bytes  %s  %3d%%  %s%s",
				site.Bytes, pad(source.Plural(site.Lines, "line"), len("128 lines")), share,
				strings.Repeat("  ", site.Depth()), site.Label())
		}
	}
	// Where each function landed in the emitted text, which is what a reader
	// holding the assembly needs to find the code a site names.
	for _, fn := range r.Functions {
		fmt.Fprintf(&b, "\n  %s: %d bytes over %s from emitted line %d, declared at %s",
			fn.Name, fn.Bytes, source.Plural(fn.Lines, "line"), fn.FirstLine, fn.Pos)
	}
	return b.String()
}

// describe states what the memory array holds and what a call frame has left to
// grow into, which is the part no other line of the report covers.
func (s SlotReport) describe() string {
	spent := fmt.Sprintf("%d of %d slots, %d of them spilled", s.Used(), MaxSlots, s.Spill)
	// Exhaustion is asked before the call question, so that a program which
	// filled the array is told so rather than told that nothing grows into the
	// nothing that is left.
	switch {
	case s.Headroom() <= 0 && !s.Frames:
		return spent + "; the array is full, and the program makes no call, so nothing needs a slot above it"
	case s.Headroom() <= 0:
		return spent + "; no slot is left for a call frame, and a frame reaching a global overwrites it with nothing trapping"
	case !s.Frames:
		return spent + fmt.Sprintf("; the program makes no call, so nothing grows into the remaining %d", s.Headroom())
	default:
		return spent + fmt.Sprintf("; %s for call frames, which nothing stops from growing past", source.Plural(s.Headroom(), "slot"))
	}
}

// pad right-aligns text in a column of width columns, so the attribution table
// reads as columns rather than as ragged prose.
func pad(text string, columns int) string {
	if len(text) >= columns {
		return text
	}
	return strings.Repeat(" ", columns-len(text)) + text
}

// siteNode is one construct while attribution is being accumulated. The tree it
// forms is the call structure the inliner flattened, rebuilt from the chain each
// line carries.
type siteNode struct {
	site     SiteReport
	children []*siteNode
}

// attribute charges every line to the construct that produced it and to every
// call that construct was reached through.
//
// Charging only the innermost site would split one call's cost across a row per
// callee it in turn expands, and the question the report answers — what does
// deleting this call save — would have to be summed by hand. Rolling the cost up
// every enclosing site is what makes a row's number the answer.
//
// Siblings are ordered by bytes and then by label, so a report is stable across
// runs and two constructs of equal size keep a fixed order.
func attribute(lines []line) []SiteReport {
	index := make(map[string]*siteNode)
	var roots []*siteNode
	for _, l := range lines {
		chain := outermostFirst(l.inline)
		var key strings.Builder
		key.WriteString(l.fn)
		var parent *siteNode
		for depth := 0; depth <= len(chain); depth++ {
			if depth > 0 {
				key.WriteString("\x00" + chain[depth-1].String())
			}
			node, seen := index[key.String()]
			if !seen {
				node = &siteNode{site: SiteReport{Func: l.fn, Chain: slices.Clone(chain[:depth])}}
				index[key.String()] = node
				if parent == nil {
					roots = append(roots, node)
				} else {
					parent.children = append(parent.children, node)
				}
			}
			node.site.Bytes += len(l.text) + 1
			node.site.Lines++
			if depth == len(chain) {
				node.site.Own += len(l.text) + 1
			}
			parent = node
		}
	}
	return flattenSites(roots, nil)
}

// flattenSites orders each level largest first and lays the tree out depth
// first, which is the order a reader scans: a call, then what it brought with
// it.
func flattenSites(nodes []*siteNode, out []SiteReport) []SiteReport {
	slices.SortStableFunc(nodes, func(a, b *siteNode) int {
		if c := cmp.Compare(b.site.Bytes, a.site.Bytes); c != 0 {
			return c
		}
		return cmp.Compare(a.site.Label(), b.site.Label())
	})
	for _, node := range nodes {
		out = append(out, node.site)
		out = flattenSites(node.children, out)
	}
	return out
}

// outermostFirst reverses a chain that runs innermost first, which is the order
// selection reads it out of the debug locations and the opposite of the order
// the source is read in.
func outermostFirst(chain []source.InlineSite) []source.InlineSite {
	reversed := slices.Clone(chain)
	slices.Reverse(reversed)
	return reversed
}

func buildReport(lines []line, funcs []FuncReport, slots SlotReport) Report {
	report := Report{
		Bytes:     measure(lines),
		Lines:     len(lines),
		Slots:     slots,
		Functions: funcs,
		Sites:     attribute(lines),
	}
	for _, l := range lines {
		report.LongestLine = max(report.LongestLine, len(l.text))
	}
	if report.Bytes > MaxBytes {
		report.Violations = append(report.Violations, Violation{
			Kind: ViolationBytes,
			Line: -1,
			Msg:  fmt.Sprintf("program is %d bytes, %d over the %d byte budget", report.Bytes, report.Bytes-MaxBytes, MaxBytes),
		})
	}
	if report.Lines > MaxLines {
		report.Violations = append(report.Violations, Violation{
			Kind: ViolationLines,
			Line: -1,
			Msg:  fmt.Sprintf("program is %d lines, %d over the %d line limit", report.Lines, report.Lines-MaxLines, MaxLines),
		})
	}
	for i, l := range lines {
		if len(l.text) <= MaxLineLength {
			continue
		}
		report.Violations = append(report.Violations, Violation{
			Kind: ViolationLineLength,
			Line: i,
			Pos:  l.pos,
			Msg:  fmt.Sprintf("line %d is %d characters, over the %d character limit", i, len(l.text), MaxLineLength),
		})
	}
	return report
}

// measure charges each line its text and the newline that ends it.
func measure(lines []line) int {
	total := 0
	for _, l := range lines {
		total += len(l.text) + 1
	}
	return total
}
