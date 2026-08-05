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

// Limits the game's editor holds a program to, from Assets.Scripts.UI.InputSourceCode. Only the byte
// budget is enforced by refusal (UpdateFileSize disables submission above 4096); the other two are
// enforced by silent truncation, which is why ViolationLineLength is fatal here rather than
// advisory: a program over the width pastes cleanly and runs as a different program than the one
// compiled. What sits past a line's first '#' is exempt, since ProgrammableChip cuts there before
// deciding what the line is; Report.TruncatedComments counts those lines instead.
const (
	// MaxBytes is the program size budget, inclusive: the editor enables its submit button at
	// exactly 4096. Which of MaxBytes and MaxLines actually binds is computed per program by
	// Report.Binding rather than assumed.
	MaxBytes = 4096
	// MaxLines is the line count limit, the budget most programs meet first. The chip's per-tick
	// instruction budget is 128 too, but that is CircuitHousing.RUN_COUNT, an unrelated constant.
	MaxLines = 128
	// MaxLineLength is the character limit for one line, the length InputSourceCode.Paste passes to
	// AsciiString.ParseLine. Emitted text is ASCII only, so a byte count is a character count.
	MaxLineLength = 90
)

// MaxSlots is the chip's whole memory: one array shared by the data region and the call frames, with
// nothing between them. It sits apart from the three limits above because the editor enforces none
// of it — see the package doc for what running off the top costs.
const MaxSlots = ic10.NumMemorySlots

// Report accounts for the size of an emitted program. Bytes is the number the editor computes, not
// the length of Text; every byte in the report is summed from the same per-line charge, so a
// function's bytes and a site's own bytes both add up to the program's.
type Report struct {
	// Bytes is the program's size as InputSourceCode.UpdateFileSize counts it: each line's text,
	// plus a two byte separator after every line that still has a line with text below it. Text is
	// shorter, joining lines with one byte and ending without a separator — that is the wrong measure; Bytes is the right one.
	Bytes int
	Lines int
	// LongestLine is the character count of the widest emitted line, comment
	// included, which is what the 90 character limit is spent against and what
	// decides whether the editor's paste cuts anything at all.
	LongestLine int
	// TruncatedComments counts the lines the editor's paste cuts inside a trailing comment, where the
	// cut takes no instruction text and the program is the one that was compiled. It is not a
	// violation, but is counted rather than left silent, since LongestLine alone would read as over
	// the limit with nothing in the report explaining why that is survivable.
	TruncatedComments int
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

// SlotReport accounts for the memory array a program was laid out against. It is both an input to
// Emit and part of its report, since no emitted line names the boundary. The zero value describes a
// program that allocates nothing and calls nothing.
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

// Headroom is the slots left for call frames to grow into. It is reported rather than ranked against
// the text limits, since no figure for it is provably enough: frame depth is bounded statically only
// for a program with no recursion, which is the only thing that forces a real call at all.
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

// SiteReport attributes part of a program's size to one construct: an emitted function, or one call
// inlined into it. A function is the wrong unit on this target, since calls are inlined by default
// and a function called from three places pays for itself three times; Chain separates the three by
// the call each came through. Sites nest: Bytes covers everything spliced in below the site, which
// is what deleting the call would recover, while Own is the code at the site alone and sums across every site to the program.
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

var violationKindNames = [...]string{
	ViolationBytes:      "bytes",
	ViolationLines:      "lines",
	ViolationLineLength: "line length",
}

var violationKindNouns = [...]string{
	ViolationBytes:      "the byte budget",
	ViolationLines:      "the line count",
	ViolationLineLength: "the line width",
}

func (k ViolationKind) String() string {
	return source.EnumName(violationKindNames[:], int(k), "ViolationKind")
}

// Noun names the limit as the subject of a sentence, which String's plural
// form is not.
func (k ViolationKind) Noun() string {
	return source.EnumName(violationKindNouns[:], int(k), "ViolationKind")
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

// Percent is the share of the limit the program spends. A share under the limit rounds down and a
// share over it rounds up, so 100 reads only for a program at exactly the limit: rounding both ways
// down would make a program one byte over budget read as merely full.
func (s Spend) Percent() int { return percent(s.Used, s.Max) }

// percent is the share used is of limit, rounded down below the limit and up above it, or 0 for a
// zero limit. Every percentage the report prints goes through here. The attribution column does not
// add up to 100 and is not meant to: a nested row's bytes are counted in its parent's too, and
// rounding each row down is what keeps a share from overstating what deleting the call recovers.
func percent(used, limit int) int {
	if limit == 0 {
		return 0
	}
	if used > limit {
		return (used*100 + limit - 1) / limit
	}
	return used * 100 / limit
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

// Binding names the size limit the program spends the largest share of, the one that decides how
// much room is left. Only the two whole-program budgets are ranked: the 90 character line width
// bounds one line's formatting, not how much program fits, so it is reported and not ranked. Which
// of the two binds is computed rather than assumed, since a program of long lines can meet the byte
// cap while under the line cap, and telling it to cut lines would be wrong. Ties go to whichever Spends lists first.
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
	if r.TruncatedComments > 0 {
		fmt.Fprintf(&b, "\n  cut on paste: %s past %d characters, in the trailing comment only; the chip cuts the line at the '#' before that, so the program is the one that was compiled",
			source.Plural(r.TruncatedComments, "line"), MaxLineLength)
	}
	if len(r.Sites) > 0 {
		b.WriteString("\n  bytes by construct, largest first; an indented call's bytes are counted in the line above it:")
		counts, width := lineCounts(r.Sites)
		for i, site := range r.Sites {
			fmt.Fprintf(&b, "\n    %5d bytes  %s  %3d%%  %s%s",
				site.Bytes, pad(counts[i], width), percent(site.Bytes, r.Bytes),
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
	// Overflow is checked before exhaustion: a data region past the end of the array is a program
	// whose own globals do not fit, not one with merely no headroom left.
	switch {
	case s.Headroom() < 0:
		return spent + fmt.Sprintf("; the data region runs %s past the end of the array, and a load past that end stops the chip with the unknown error and a store with a stack overflow",
			source.Plural(-s.Headroom(), "slot"))
	case s.Headroom() == 0 && !s.Frames:
		return spent + "; the array is full, and the program makes no call, so nothing needs a slot above it"
	case s.Headroom() == 0:
		return spent + "; no slot is left for a call frame, and the first push a frame makes stops the chip with a stack overflow"
	case !s.Frames:
		return spent + fmt.Sprintf("; the program makes no call, so nothing grows into the remaining %d", s.Headroom())
	default:
		return spent + fmt.Sprintf("; %s for call frames, and the push past the last of them stops the chip with a stack overflow", source.Plural(s.Headroom(), "slot"))
	}
}

// lineCounts renders the attribution table's line count cells and the width to set that column to.
// The width starts at what the line limit spells, so reports of different sizes align on the same
// column, but is a floor rather than the width: a program over the line limit needs more.
func lineCounts(sites []SiteReport) (cells []string, width int) {
	cells = make([]string, len(sites))
	width = len(source.Plural(MaxLines, "line"))
	for i, site := range sites {
		cells[i] = source.Plural(site.Lines, "line")
		width = max(width, len(cells[i]))
	}
	return cells, width
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

// attribute charges every line to the construct that produced it and to every call that construct
// was reached through, so a row's number directly answers what deleting that call would save (charging
// only the innermost site would split one call's cost across a row per callee it expands).
func attribute(lines []line, costs []int) []SiteReport {
	index := make(map[string]*siteNode)
	var roots []*siteNode
	for i, l := range lines {
		chain := outermostFirst(l.inline)
		var key strings.Builder
		key.WriteString(strconv.Itoa(l.fnOrdinal))
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
			node.site.Bytes += costs[i]
			node.site.Lines++
			if depth == len(chain) {
				node.site.Own += costs[i]
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

// outermostFirst reverses a chain that runs innermost first, the order selection reads it out of the
// debug locations. It copies rather than reverses in place, which is what [Emit]'s guarantee that it
// leaves the program alone rests on: a line's chain is the instruction's own slice.
func outermostFirst(chain []source.InlineSite) []source.InlineSite {
	reversed := slices.Clone(chain)
	slices.Reverse(reversed)
	return reversed
}

// buildReport takes ownership of funcs, whose Bytes it fills in from the charges
// it computes here.
func buildReport(lines []line, funcs []FuncReport, slots SlotReport) Report {
	costs := charges(lines)
	report := Report{
		Bytes:     sum(costs),
		Lines:     len(lines),
		Slots:     slots,
		Functions: funcs,
		Sites:     attribute(lines, costs),
	}
	// Charged here rather than as each function is laid out: what a line costs
	// depends on whether a later function still has text below it.
	for i := range report.Functions {
		fn := &report.Functions[i]
		fn.Bytes = sum(costs[fn.FirstLine : fn.FirstLine+fn.Lines])
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
		width := codeWidth(l.text)
		if width <= MaxLineLength {
			report.TruncatedComments++
			continue
		}
		report.Violations = append(report.Violations, Violation{
			Kind: ViolationLineLength,
			Line: i,
			Pos:  l.pos,
			Msg:  fmt.Sprintf("line %d holds %d characters of instruction, over the %d character limit", i, width, MaxLineLength),
		})
	}
	return report
}

// codeWidth is the width the editor's truncation has to be measured against: the characters of text
// the chip will read as an instruction. ProgrammableChip._LineOfCode cuts a line at its first '#'
// before splitting it, so a paste past that point takes comment text and changes nothing the chip runs.
func codeWidth(text string) int {
	code, _, commented := strings.Cut(text, "#")
	if !commented {
		return len(text)
	}
	return len(strings.TrimRight(code, " "))
}

// separatorBytes is what the editor charges between two lines: UpdateFileSize increments _fileSize
// twice per charged line and reads no newline sequence at all. It is the game's own literal, not a
// platform separator width — Environment.NewLine is one byte on Linux — and correcting it to match
// the platform would undercount every multi-line program against what the editor shows.
const separatorBytes = 2

// lastText is the index of the last line holding any text, which is the line the editor stops
// charging separators before. It is 0 for a program with no text at all, matching UpdateFileSize's
// own backward scan, which runs to index 0 and keeps whatever it stopped on. Emit never produces a
// blank line, so for real output this is always the final index; it is written as the scan anyway
// because it models UpdateFileSize, and the two answers differ only for the synthetic fixtures pinning that model.
func lastText(lines []line) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].text != "" {
			return i
		}
	}
	return 0
}

// charges is what the editor's size counter adds for each line: the text the paste stored (capped at
// MaxLineLength, since Paste runs each line through AsciiString.ParseLine before UpdateFileSize sees
// it), plus a separator while a line below it still has text. Past MaxLines this charges every
// emitted line rather than only the ones Paste's fixed grid would receive, which is the larger and
// more useful figure for a program already reported over the line limit.
func charges(lines []line) []int {
	last := lastText(lines)
	costs := make([]int, len(lines))
	for i, l := range lines {
		costs[i] = min(len(l.text), MaxLineLength)
		if i < last {
			costs[i] += separatorBytes
		}
	}
	return costs
}

func sum(costs []int) int {
	total := 0
	for _, cost := range costs {
		total += cost
	}
	return total
}
