package devtrace

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/ic10"
)

// hostSource implements the intrinsics for a native build. It is checked in
// under testdata so the Go tool leaves it alone.
//
//go:embed testdata/host.c
var hostSource string

// microcEntry is what a fixture's main is compiled under, so that the harness
// owns the process entry point and can start and stop a program whose own loop
// never ends.
const microcEntry = "ic_microc_main"

// cCompiler is the driver the native build runs: clang alone, since the
// argument file the corpus declares is clang's.
const cCompiler = "clang"

// nativeFlags are what the native build adds to the argument file the corpus
// declares: the optimizer off, so the build stays the least rewritten
// translation of the source; contraction off, since the machine has no fused
// multiply-add; the undefined-behaviour sanitizer on with recovery off, so a
// program that hits one stops and is reported.
var nativeFlags = []string{
	"-O0",
	"-ffp-contract=off",
	"-fsanitize=undefined",
	"-fno-sanitize-recover=all",
}

// RunNative compiles a MicroC source file as a native program and records
// what it writes to h.
//
// Intrinsics are answered by the game's own chip running the instruction each
// one names, over h's housing, so only the program's control flow and
// arithmetic differ from a chip run. A segment that keeps asking fails at
// [segmentBudget] intrinsic calls; one that stops asking fails after
// [nativeStall]. A missing compiler, a failed build, a sanitizer report and a
// crash all fail the test rather than being skipped.
func RunNative(ctx context.Context, t *testing.T, h *chip.FixtureHarness, source string, opts RunOptions) Trace {
	t.Helper()
	checkSegments(t, opts)
	binary := buildNative(t, source)
	host := newHostChip(ctx, t, h, opts)

	cmd := exec.CommandContext(t.Context(), binary)
	// The request stream is opened here rather than taken from StdoutPipe, so
	// that the harness holds the reading end as a file and can put a deadline on
	// it. A read with no deadline is what leaves a program that stops asking
	// waiting for the test timeout.
	requests, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("%s: opening the program's request stream: %v", opts.Name, err)
	}
	defer func() {
		if err := requests.Close(); err != nil {
			t.Errorf("%s: closing the request stream: %v", opts.Name, err)
		}
	}()
	cmd.Stdout = writer
	replies, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("%s: connecting to the program's replies: %v", opts.Name, err)
	}
	var diagnostics strings.Builder
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: starting %s: %v", opts.Name, binary, err)
	}
	// The parent's copy of the writing end goes now that the program holds one,
	// so that a program which exits leaves the harness at an end of file rather
	// than on a stream nothing will write to again.
	if err := writer.Close(); err != nil {
		t.Errorf("%s: closing the harness's copy of the request stream: %v", opts.Name, err)
	}

	run := &nativeRun{
		ctx:      ctx,
		t:        t,
		host:     host,
		opts:     opts,
		out:      bufio.NewWriter(replies),
		in:       bufio.NewScanner(requests),
		requests: requests,
	}
	trace := run.serve()

	// The reply stream is closed before waiting, so a program that stopped
	// asking cannot leave the harness blocked on a process that is blocked on
	// it.
	if err := replies.Close(); err != nil && !gone(err) {
		t.Errorf("%s: closing the reply stream: %v", opts.Name, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s: the native build of %s did not run to completion: %v\n%s",
			opts.Name, filepath.Base(source), err, diagnostics.String())
	}
	if report := strings.TrimSpace(diagnostics.String()); report != "" {
		t.Errorf("%s: the native build of %s wrote diagnostics, which the sanitizer uses to report behaviour C leaves undefined:\n%s",
			opts.Name, filepath.Base(source), report)
	}
	return trace
}

// buildNative compiles source and the host implementation into one program.
// source is compiled with the corpus's own argument file; the host is not,
// since it is not MicroC and needs the hosted library that flag excludes.
func buildNative(t *testing.T, source string) string {
	t.Helper()
	compiler, err := exec.LookPath(cCompiler)
	if err != nil {
		t.Fatalf("%s is not on PATH, and the clang comparison cannot run without it: %v", cCompiler, err)
	}

	dir := t.TempDir()
	header := filepath.Join(dir, ic10.PreludeFileName)
	host := filepath.Join(dir, "host.c")
	for path, contents := range map[string]string{header: ic10.Prelude, host: hostSource} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	program := filepath.Join(dir, "program")
	compile(t, compiler, append(append(fixtureArgs(t, header), nativeFlags...),
		"-Dmain="+microcEntry, "-c", source, "-o", filepath.Join(dir, "program.o")))
	compile(t, compiler, append(slices.Clone(nativeFlags),
		"-std=c23", "-I", dir, "-c", host, "-o", filepath.Join(dir, "host.o")))
	compile(t, compiler, append(slices.Clone(nativeFlags),
		filepath.Join(dir, "program.o"), filepath.Join(dir, "host.o"), "-o", program))
	return program
}

func compile(t *testing.T, compiler string, args []string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), compiler, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(compiler), strings.Join(args, " "), err, out)
	}
	if len(out) > 0 {
		t.Logf("%s %s:\n%s", filepath.Base(compiler), strings.Join(args, " "), out)
	}
}

// fixtureArgs renders the argv a corpus program is compiled with: the checked-in
// argument file, with its relative header path replaced by the copy written
// beside this build.
func fixtureArgs(t *testing.T, header string) []string {
	t.Helper()
	var args []string
	included := 0
	for line := range strings.Lines(ic10.CompileFlags) {
		arg := strings.TrimSpace(line)
		switch arg {
		case "":
		case ic10.PreludeFileName:
			args = append(args, header)
			included++
		default:
			args = append(args, arg)
		}
	}
	if included != 1 {
		t.Fatalf("%s names %s %d times, want once", ic10.CompileFlagsFileName, ic10.PreludeFileName, included)
	}
	return args
}

// nativeStall bounds the wait for one request, catching a segment that stops
// asking rather than one that keeps asking (that is [segmentBudget]'s job). It
// is far longer than any working program spends between two requests.
const nativeStall = time.Minute

// nativeRun is one native program under the harness's control.
type nativeRun struct {
	ctx  context.Context
	t    *testing.T
	host *hostChip
	opts RunOptions
	out  *bufio.Writer
	in   *bufio.Scanner
	// requests is what in reads, held as a file so that a wait for the next
	// request can be given a deadline.
	requests *os.File
	// segments counts the yields the program has reached, which is what a
	// segment is on this side.
	segments int
	// calls counts the intrinsics the current segment has asked for, which is
	// what bounds a segment with no yield in it.
	calls int
	stop  Stop
}

// serve answers the program's requests until the run ends, applying the
// stimulus before the first segment and before each one after, as [Run] does.
func (r *nativeRun) serve() Trace {
	r.t.Helper()
	r.stimulate(0)
	for r.scan() {
		if done := r.dispatch(r.in.Text()); done {
			break
		}
	}
	switch err := r.in.Err(); {
	case errors.Is(err, os.ErrDeadlineExceeded):
		r.t.Fatalf("%s: segment %d asked for nothing in %s, so it is running code that reaches neither an intrinsic nor a yield, and the two runs are no longer at the same place in the source",
			r.opts.Name, r.segments, nativeStall)
	case err != nil:
		r.t.Fatalf("%s: reading the program's requests: %v", r.opts.Name, err)
	}
	// An unset ending means the request stream simply ended without the host
	// reporting one; the exit status alone does not catch it, since such a
	// build exits cleanly.
	if r.stop.Reason == stopUnset {
		r.t.Fatalf("%s: the program stopped asking after %d segments without reaching an ending, so it left its entry point some way the host does not report",
			r.opts.Name, r.segments)
	}
	writes, err := r.host.harness.Trace(r.ctx)
	if err != nil {
		r.t.Fatalf("%s: reading the trace: %v", r.opts.Name, err)
	}
	return Trace{Name: r.opts.Name, Events: writes, Segments: r.segments, Stop: r.stop,
		producer: producerNative}
}

// scan reads the next request under [nativeStall], so that a program which has
// stopped asking is reported rather than waited on.
func (r *nativeRun) scan() bool {
	r.t.Helper()
	if err := r.requests.SetReadDeadline(time.Now().Add(nativeStall)); err != nil {
		r.t.Fatalf("%s: bounding the wait for a request: %v", r.opts.Name, err)
	}
	return r.in.Scan()
}

// dispatch answers one request, reporting whether the run has ended.
func (r *nativeRun) dispatch(request string) bool {
	r.t.Helper()
	kind, rest, _ := strings.Cut(request, " ")
	switch kind {
	case "y":
		return r.yield()
	case "end":
		// The program's main returned, which is where the chip's program
		// counter leaves its own instructions. The segment it ended in counts,
		// as it does there.
		r.segments++
		r.stop = Stop{Reason: StopEnded}
		return true
	case "sleep":
		r.t.Fatalf("%s: __ic_sleep suspends the chip across ticks, which is a segment boundary the host has no counterpart for; this program has no native run to compare", r.opts.Name)
		return true
	}

	r.calls++
	if r.calls > segmentBudget {
		r.t.Fatalf("%s: segment %d asked for %d intrinsics without reaching a yield, so the two runs are no longer at the same place in the source",
			r.opts.Name, r.segments, r.calls)
	}
	value, fault, err := r.host.call(r.ctx, kind, fields(r.t, r.opts.Name, kind, rest))
	if err != nil {
		r.t.Fatalf("%s: %s: %v", r.opts.Name, request, err)
	}
	if fault.Type != chip.ExcNone {
		// The chip's program would fault on the same intrinsic, so the run ends
		// here with the faulting segment counted, as [Run] counts it too.
		r.segments++
		r.reply(stopReply)
		r.stop = Stop{Reason: StopFaulted, Fault: fault.Type, Line: fault.Line}
		return true
	}
	r.reply(valueMarker + strconv.FormatUint(math.Float64bits(value), 16))
	return false
}

// yield ends a segment, reporting whether that was the last one the run was
// given.
func (r *nativeRun) yield() bool {
	r.segments++
	r.calls = 0
	if r.segments >= r.opts.Segments {
		r.reply(stopReply)
		r.stop = Stop{Reason: StopSegments}
		return true
	}
	r.stimulate(r.segments)
	r.reply(".")
	return false
}

func (r *nativeRun) stimulate(segment int) {
	r.t.Helper()
	if r.opts.Stimulus != nil {
		r.opts.Stimulus(r.t, r.host.harness, segment)
	}
}

// valueMarker introduces a double's bit pattern, on a request and on a reply,
// so a NaN payload and the sign of a zero cross the wire intact.
const valueMarker = "#"

// stopReply ends the run. Any request may be answered with it, because a
// MicroC control loop has no other way out and a run can stop at one.
const stopReply = "x"

// reply answers one request. A write that fails because the program is gone
// is not reported: the next read ends the loop, and the exit status waited
// for afterwards says why.
func (r *nativeRun) reply(line string) {
	r.t.Helper()
	if _, err := r.out.WriteString(line + "\n"); err != nil && !gone(err) {
		r.t.Fatalf("%s: answering the program: %v", r.opts.Name, err)
	}
	if err := r.out.Flush(); err != nil && !gone(err) {
		r.t.Fatalf("%s: answering the program: %v", r.opts.Name, err)
	}
}

// gone reports whether a write failed because the program is no longer there.
func gone(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE)
}

// fields parses a request's arguments. A value carries its bit pattern behind
// [valueMarker]; an operand the program cannot have computed as a double, such
// as a pin or a hash, is written as an integer. Both reach the chip as the
// double a register holds.
func fields(t *testing.T, name, kind, rest string) []float64 {
	t.Helper()
	var out []float64
	for field := range strings.FieldsSeq(rest) {
		if pattern, ok := strings.CutPrefix(field, valueMarker); ok {
			bits, err := strconv.ParseUint(pattern, 16, 64)
			if err != nil {
				t.Fatalf("%s: %s carries the value %q, which is not a bit pattern", name, kind, field)
			}
			out = append(out, math.Float64frombits(bits))
			continue
		}
		n, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			t.Fatalf("%s: %s carries the argument %q, which is not an integer", name, kind, field)
		}
		out = append(out, float64(n))
	}
	return out
}

// hostChip answers a native program's intrinsics by running the instruction
// each one names against the same housing a chip run uses, so no intrinsic
// gets a second implementation the two runs could disagree without either
// program being wrong.
type hostChip struct {
	harness *chip.FixtureHarness
	// line is the dispatch program line each request kind runs, and operands is
	// how many arguments a request for it carries.
	line     map[string]int
	operands map[string]int
	// selfProperties maps each of [housingSelfProperties] to its name, keyed by
	// the number a program names it with.
	selfProperties map[float64]string
}

// dispatchLine is one request kind and the instruction it runs.
type dispatchLine struct {
	kind string
	code string
}

// operands is how many arguments a request for this line carries: the highest
// register the instruction names, since nothing clears the registers above it
// and a request one short would run against whatever the last intrinsic left
// there. A form spelling the housing out still carries the pin, which is what
// the floor of one covers.
func (d dispatchLine) operands(t *testing.T) int {
	t.Helper()
	fields := strings.Fields(d.code)
	if len(fields) == 0 {
		t.Fatalf("the dispatch line for %q names no instruction", d.kind)
	}
	count, device := 0, false
	for _, operand := range fields[1:] {
		if operand == "db" {
			device = true
			continue
		}
		name, indirect := strings.CutPrefix(operand, "d")
		index, ok := registerIndex(name)
		if !ok {
			t.Fatalf("the dispatch line for %q takes the operand %q, which is neither a register nor the housing, so what a request for it carries cannot be read off it",
				d.kind, operand)
		}
		device = device || indirect
		count = max(count, index)
	}
	if device {
		count = max(count, 1)
	}
	return count
}

// registerIndex reads a general register operand's index.
func registerIndex(operand string) (int, bool) {
	digits, ok := strings.CutPrefix(operand, "r")
	if !ok {
		return 0, false
	}
	index, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return index, true
}

// dispatchLines is the request kinds a native program makes, and the
// instruction each one runs. Arguments are taken from r1 upward in the order
// the request carries them, so a form that names no pin simply leaves r1 alone.
var dispatchLines = slices.Concat(deviceForms, []dispatchLine{randomDraw}, machineFunctions)

// deviceForms are the requests answered out of the world.
var deviceForms = []dispatchLine{
	{"l", "l r0 dr1 r2"},
	{"s", "s dr1 r2 r3"},
	{"ls", "ls r0 dr1 r2 r3"},
	{"ss", "ss dr1 r2 r3 r4"},
	{"lr", "lr r0 dr1 r2 r3"},
	{"dse", "sdse r0 dr1"},

	// The housing is not a pin, so each form that can name it needs its own line.
	{"l" + chipSuffix, "l r0 db r2"},
	{"s" + chipSuffix, "s db r2 r3"},
	{"ls" + chipSuffix, "ls r0 db r2 r3"},
	{"ss" + chipSuffix, "ss db r2 r3 r4"},
	{"lr" + chipSuffix, "lr r0 db r2 r3"},
	{"dse" + chipSuffix, "sdse r0 db"},

	{"lb", "lb r0 r1 r2 r3"},
	{"sb", "sb r1 r2 r3"},
	{"lbn", "lbn r0 r1 r2 r3 r4"},
	{"sbn", "sbn r1 r2 r3 r4"},
	{"lbs", "lbs r0 r1 r2 r3 r4"},
	{"sbs", "sbs r1 r2 r3 r4"},
	{"lbns", "lbns r0 r1 r2 r3 r4 r5"},
}

// randomDraw is `rand`, which is neither a device form nor a machine function:
// it answers out of a seeded source rather than out of the world or out of its
// operands, so no program's answer can be held to it.
var randomDraw = dispatchLine{kind: "rand", code: "rand r0"}

// machineFunctions are the machine's own functions, which compute an answer
// from their operands alone. That is what lets a program calling one be held to
// a value written down.
var machineFunctions = []dispatchLine{
	{"sqrt", "sqrt r0 r1"},
	{"abs", "abs r0 r1"},
	{"sgn", "sgn r0 r1"},
	{"round", "round r0 r1"},
	{"trunc", "trunc r0 r1"},
	{"ceil", "ceil r0 r1"},
	{"floor", "floor r0 r1"},
	{"log", "log r0 r1"},
	{"exp", "exp r0 r1"},
	{"sin", "sin r0 r1"},
	{"cos", "cos r0 r1"},
	{"tan", "tan r0 r1"},
	{"asin", "asin r0 r1"},
	{"acos", "acos r0 r1"},
	{"atan", "atan r0 r1"},

	{"min", "min r0 r1 r2"},
	{"max", "max r0 r1 r2"},
	{"pow", "pow r0 r1 r2"},
	{"atan2", "atan2 r0 r1 r2"},

	{"clamp", "clamp r0 r1 r2 r3"},
	{"lerp", "lerp r0 r1 r2 r3"},
}

// machineFunctionNames names the machine's own functions a native program may
// ask for, derived from the dispatch table rather than restated beside it.
// `rand` is not among them.
func machineFunctionNames() []string {
	out := make([]string, len(machineFunctions))
	for i, entry := range machineFunctions {
		out[i] = entry.kind
	}
	return out
}

// chipPin is the pin the prelude's `db` enumerator carries, and chipSuffix
// marks the dispatch line a request naming it runs. The chip names the
// housing with an index no register operand can reach, so a form that can name
// it needs a line spelling it out.
const (
	chipPin    = -1
	chipSuffix = "@db"
)

// chipLoadKind is the request that reads a logic property off the housing,
// which is the one form whose answer can be the asking chip's own state.
const chipLoadKind = "l" + chipSuffix

// housingSelfProperties are the housing properties answered out of the chip in
// the housing (LineNumber, Error) rather than out of the world. They are the
// only place the two builds do not share the housing: a native build has no
// chip, so the dispatch program would answer for its own state instead of the
// program under test's.
var housingSelfProperties = []string{"LineNumber", "Error"}

func newHostChip(ctx context.Context, t *testing.T, h *chip.FixtureHarness, opts RunOptions) *hostChip {
	t.Helper()
	program := make([]string, len(dispatchLines))
	line := make(map[string]int, len(dispatchLines))
	operands := make(map[string]int, len(dispatchLines))
	for i, entry := range dispatchLines {
		program[i] = entry.code
		if _, taken := line[entry.kind]; taken {
			t.Fatalf("two dispatch lines run %q, and the later one would silently answer every request for it", entry.kind)
		}
		line[entry.kind] = i
		operands[entry.kind] = entry.operands(t)
	}

	if err := h.Reset(ctx); err != nil {
		t.Fatalf("%s: resetting the chip: %v", opts.Name, err)
	}
	if opts.World != nil {
		opts.World(t, h)
	}
	// No clock: `sleep` is the one operation that reads one, and a request for
	// it never reaches dispatch.
	if err := h.SetRandomSeed(ctx, randomSeed); err != nil {
		t.Fatalf("%s: arming the generator: %v", opts.Name, err)
	}
	if err := h.Load(ctx, strings.Join(program, "\n")); err != nil {
		t.Fatalf("the intrinsic dispatch program does not load: %v", err)
	}

	self := make(map[float64]string, len(housingSelfProperties))
	for _, name := range housingSelfProperties {
		info, ok := ic10.LookupLogicType(name)
		if !ok {
			t.Fatalf("the machine tables declare no %s, and the dispatch program refuses a request reading it off the housing by its number", name)
		}
		self[float64(info.Value)] = name
	}
	return &hostChip{harness: h, line: line, operands: operands, selfProperties: self}
}

// call runs one intrinsic and returns what it answered, along with whatever
// the chip faulted with. The fault is a value rather than an error because it
// is a chip verdict, matching what the compiled build would do on the same
// intrinsic; an error is reserved for a request this package cannot run at
// all. A request must carry exactly the operands its dispatch line reads,
// since a short one would silently answer out of a register the previous
// intrinsic wrote.
func (h *hostChip) call(ctx context.Context, kind string, args []float64) (float64, chip.Fault, error) {
	key := kind
	if len(args) > 0 && args[0] == chipPin {
		if _, named := h.line[kind+chipSuffix]; named {
			key = kind + chipSuffix
		}
	}
	line, ok := h.line[key]
	if !ok {
		return 0, chip.Fault{}, fmt.Errorf("no dispatch line runs %q", key)
	}
	if want := h.operands[key]; len(args) != want {
		return 0, chip.Fault{}, fmt.Errorf("a request for %q carries %d operands and the line running it reads %d", key, len(args), want)
	}
	if key == chipLoadKind {
		if name, self := h.selfProperties[args[1]]; self {
			return 0, chip.Fault{}, fmt.Errorf("a request reads %s off the housing, which is answered out of the chip in it rather than out of the world; the compiled build reads its own state there and this build would read the dispatch program's, so the two runs would be reading different programs", name)
		}
	}

	// r0 is written along with the arguments so a store's stale value there
	// cannot be returned as though the dispatch line had computed it.
	registers := make([]float64, len(args)+1)
	copy(registers[1:], args)
	if err := h.harness.SetRegisters(ctx, registers...); err != nil {
		return 0, chip.Fault{}, fmt.Errorf("seeding the operands of %q: %w", key, err)
	}
	if err := h.harness.SetAddress(ctx, line); err != nil {
		return 0, chip.Fault{}, fmt.Errorf("selecting the dispatch line for %q: %w", key, err)
	}
	segment, err := h.harness.Step(ctx, 1)
	if err != nil {
		return 0, chip.Fault{}, fmt.Errorf("running the dispatch line for %q: %w", key, err)
	}
	if segment.CompileError.Type != chip.ExcNone {
		return 0, chip.Fault{}, fmt.Errorf("the dispatch program did not compile: %s", segment.CompileError)
	}
	return segment.Registers[0], segment.Fault, nil
}
