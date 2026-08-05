package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/spf13/cobra"
)

// byteBudgetSource is a program past the 4096 byte budget and inside both
// the 128 line limit and the 90 character one. Each store writes a distinct
// fraction, and the chip's number parser admits no exponent, so every
// operand is written out in full without being over-long.
func byteBudgetSource(stores int) string {
	var b strings.Builder
	b.WriteString("void main(void) {\n")
	for i := range stores {
		fmt.Fprintf(&b, "\t__ic_store(d0, Setting, %de-40);\n", i+1)
	}
	b.WriteString("}\n")
	return b.String()
}

// lineLengthSource is a program past the 90 character limit and inside the other
// two. The value is small enough that writing it without an exponent takes more
// characters than a line may hold.
const lineLengthSource = "void main(void) { __ic_store(d0, Setting, 1e-100); }\n"

// deprecatedLogicType is a logic type the game marks deprecated. Which one it is
// does not matter, only that naming it draws a warning onto a run that also
// exceeds a limit.
const deprecatedLogicType = "ImportQuantity"

// warnedOverBudgetSource is a program that both warns and exceeds a limit.
func warnedOverBudgetSource(stores int) string {
	const opening = "void main(void) {\n"
	return strings.Replace(overBudgetSource(stores), opening,
		opening+"\t__ic_store(d0, "+deprecatedLogicType+", 1);\n", 1)
}

// annotatedWideSource is a program whose shipped form sits well inside every
// limit and whose readable form carries lines past the 90 character width:
// the function name itself is longer than the width, and the recursion
// keeps the function out of its caller, which an inline has no call to annotate.
var annotatedWideSource = fmt.Sprintf(`double %[1]s(double n) {
  if (n < 1) return 1;
  return n * %[1]s(n - 1);
}
void main(void) { __ic_store(d0, Setting, %[1]s(5)); }
`, "f_"+strings.Repeat("x", emit.MaxLineLength))

// fullDataRegionSource fills the 512 slot memory array the data region and the
// call frames share, and makes no call, so nothing wants a slot above it.
const fullDataRegionSource = "double a[512];\n" +
	"void main(void) {\n" +
	"  for (long long i = 0; i < 512; i++) a[i] = i;\n" +
	"  __ic_store(d0, Setting, a[511]);\n" +
	"}\n"

// streamShape is what a status promises about the output stream.
type streamShape uint8

const (
	// streamNothing says a redirection caught nothing, because the status names
	// a failure that left no program to keep.
	streamNothing streamShape = iota
	// streamProgram says the stream holds the complete assembly of a program
	// that emitted at least one instruction.
	streamProgram
	// streamEmptyProgram says the stream holds the complete assembly of a
	// program whose functions held no instruction, which is no text at all —
	// indistinguishable from streamNothing by the stream alone, so it is
	// spelled separately rather than passing for a failure.
	streamEmptyProgram
)

// requireStdout holds the output stream to what the status says is on it:
// the complete assembly of the compiled program under exitOK, and nothing
// at all under every other status — a case claiming streamEmptyProgram is
// held to exitOK, and one claiming streamNothing to a failing status.
func requireStdout(t *testing.T, status int, stdout string, want streamShape) {
	t.Helper()
	carries := status == exitOK
	if want != streamProgram {
		if stdout != "" {
			t.Errorf("the output stream holds %q, and this case says a redirection caught no text", stdout)
		}
		if want == streamEmptyProgram && !carries {
			t.Errorf("exit status %d says a redirection caught nothing to keep, and this case says the stream holds a complete program that assembles to no text", status)
		}
		if want == streamNothing && carries {
			t.Errorf("exit status %d says the stream holds a complete program, and this case says the run left none", status)
		}
		return
	}
	switch {
	case strings.TrimSpace(stdout) == "":
		t.Errorf("the output stream is empty, and this status says it holds a complete program")
	case !carries:
		t.Errorf("exit status %d says a redirection caught nothing, and the stream holds a program", status)
	case !strings.HasSuffix(stdout, "\n"):
		t.Errorf("the assembly does not end in a newline, so a redirection leaves a file whose last line is unterminated")
	default:
		checkAssembly(t, stdout)
	}
}

// TestExitStatus holds every outcome to the status it leaves and to whether
// the output stream carries a program, checked together since a redirected
// caller has only the status and the file. No case here leaves exitInternal:
// that status is a compiler defect, covered by TestPanicLeavesTheInternalStatus.
func TestExitStatus(t *testing.T) {
	tests := []struct {
		name string
		// src is written to a temporary file whose path is appended to args, so
		// that a case can pair a source with a flag. A case naming a source file
		// in args directly leaves it empty.
		src  string
		args []string
		want int
		// stdout says what the output stream must carry. The zero value is
		// streamNothing, which is what every failing case leaves.
		stdout streamShape
	}{
		{
			name:   "a program inside every limit",
			args:   []string{filepath.Join(fixtures, "thermostat.c")},
			want:   exitOK,
			stdout: streamProgram,
		},
		{
			name: "a program naming something that was never declared",
			src:  "void main(void) { undeclared_name(); }\n",
			want: exitFailure,
		},
		{
			name: "a program the backend refuses with a diagnostic",
			args: []string{filepath.Join(refusalWitnessDir, "sunk-loads.c")},
			want: exitFailure,
		},
		{
			name: "a source file that does not exist",
			args: []string{filepath.Join(t.TempDir(), "absent.c")},
			want: exitFailure,
		},
		{
			name: "a directory given where a source file goes",
			args: []string{t.TempDir()},
			want: exitFailure,
		},
		{
			name: "a flag the command does not have",
			args: []string{"--no-such-flag", filepath.Join(fixtures, "thermostat.c")},
			want: exitUsage,
		},
		{
			name: "no source file at all",
			want: exitUsage,
		},
		{
			name: "two source files",
			args: []string{filepath.Join(fixtures, "thermostat.c"), filepath.Join(fixtures, "bits.c")},
			want: exitUsage,
		},
		{
			name: "a program over the 128 line limit",
			src:  overBudgetSource(140),
			want: exitFailure,
		},
		{
			name: "a program over the 4096 byte budget",
			src:  byteBudgetSource(63),
			want: exitFailure,
		},
		{
			name: "a program over the 90 character line limit",
			src:  lineLengthSource,
			want: exitFailure,
		},
		{
			// The annotation is comment text, which the chip cuts the line at
			// before reading it, so a readable form wider than the shipped
			// one is still the shipped program: only the instruction ahead
			// of the '#' is held to the width.
			name:   "a readable form whose annotations carry lines past the width limit",
			src:    annotatedWideSource,
			args:   []string{"--readable"},
			want:   exitOK,
			stdout: streamProgram,
		},
		{
			name: "a program that both warns and is over a limit",
			src:  warnedOverBudgetSource(140),
			want: exitFailure,
		},
		{
			// The far edge of the array: every slot is spoken for and
			// nothing wants one above them. A program filling it that does
			// make a call needs a slot for the return address, and is
			// refused instead.
			name:   "a data region filling the array with no call above it",
			src:    fullDataRegionSource,
			want:   exitOK,
			stdout: streamProgram,
		},
		{
			// Complete assembly and no text are the same thing here, and the
			// status is the only thing separating this from a refusal.
			name:   "a program whose functions hold no instruction",
			src:    "void main(void) { }\n",
			want:   exitOK,
			stdout: streamEmptyProgram,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if tt.src != "" {
				args = append(slices.Clone(args), write(t, "exit_status.c", tt.src))
			}
			stdout, stderr, err := run(t, args...)
			got := exitCodeFor(err)
			if got != tt.want {
				t.Errorf("exit status %d, want %d: %v\n%s", got, tt.want, err, stderr)
			}
			requireStdout(t, got, stdout, tt.stdout)
		})
	}
}

// TestWarningLeavesTheStatusAlone separates the two halves of the case above: a
// warning on its own must not move the status off exitOK, or the case that both
// warns and exceeds a limit would pass for the wrong reason.
func TestWarningLeavesTheStatusAlone(t *testing.T) {
	src := "void main(void) { __ic_store(d0, " + deprecatedLogicType + ", 1); }\n"
	stdout, stderr, err := run(t, write(t, "warned.c", src))
	if got := exitCodeFor(err); got != exitOK {
		t.Fatalf("exit status %d for a program that only warns, want %d: %v\n%s", got, exitOK, err, stderr)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Fatalf("no warning was printed for %s:\n%s", deprecatedLogicType, stderr)
	}
	requireStdout(t, exitCodeFor(err), stdout, streamProgram)
}

// TestAnnotationPastTheWidthDoesNotMoveTheStatus holds --readable to
// costing bytes and width and nothing else: the 90 character limit is
// enforced by silent truncation, and a cut past a line's first '#' takes
// nothing, so a readable form must not be refused for width it never spends.
func TestAnnotationPastTheWidthDoesNotMoveTheStatus(t *testing.T) {
	path := write(t, "annotated.c", annotatedWideSource)

	_, shippedErr, err := run(t, path)
	if err != nil {
		t.Fatalf("the shipped form of this program has to fit every limit for the readable form to say anything: %v\n%s", err, shippedErr)
	}
	if shipped := sizeReport(t, shippedErr); shipped.LongestLine > emit.MaxLineLength {
		t.Fatalf("the shipped form is already %d characters wide, so the readable form's width is not the annotation's", shipped.LongestLine)
	}

	stdout, stderr, err := run(t, "--readable", path)
	if got := exitCodeFor(err); got != exitOK {
		t.Errorf("exit status %d for a program whose only width past the limit is comment text, want %d: %v\n%s", got, exitOK, err, stderr)
	}
	if strings.Contains(stderr, overLimitDiagnostic) {
		t.Errorf("the report names a limit violation for width the chip never reads:\n%s", stderr)
	}
	if !strings.Contains(stderr, "cut on paste") {
		t.Errorf("the report does not say the paste cuts these lines, so nothing explains a longest line past the limit beside a zero status:\n%s", stderr)
	}
	// checkAssembly holds each line's instruction text to the width, so what is
	// left to establish is that the annotation carried the whole line past it.
	widest := 0
	for _, line := range checkAssembly(t, stdout) {
		widest = max(widest, len(line))
	}
	if widest <= emit.MaxLineLength {
		t.Errorf("the widest readable line is %d characters, inside the %d character limit, so this program does not reach the case it is here for", widest, emit.MaxLineLength)
	}
}

// TestUnclassifiedErrorIsAUsageError pins the status an error reaching main
// without one leaves. Cobra raises those before the compiler runs, from a
// command line it could not read, and a case above covers each one it produces;
// this states the rule they rest on.
func TestUnclassifiedErrorIsAUsageError(t *testing.T) {
	if got := exitCodeFor(fmt.Errorf("nothing attached a status to this")); got != exitUsage {
		t.Errorf("exit status %d for an error carrying none, want %d", got, exitUsage)
	}
}

// TestExitStatusNumbersArePublished pins each status to the integer a
// caller matches on: every other case names the constant, and
// docs/compiler.md publishes 0, 1, 2 and 70 as the contract a script reads.
func TestExitStatusNumbersArePublished(t *testing.T) {
	tests := []struct {
		name string
		got  int
		want int
	}{
		{name: "a program compiled and inside every limit", got: exitOK, want: 0},
		{name: "a run that produced no program to keep", got: exitFailure, want: 1},
		{name: "a command line the command could not read", got: exitUsage, want: 2},
		{name: "a stage of the compiler that could not run", got: exitInternal, want: 70},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s leaves %d, and %d is what the contract publishes", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestEmitFileStatusFollowsTheProgram drives the compile step through the
// one entry point the command uses, and holds a program it will not build
// apart from one it does: both leave the output stream empty or full
// exactly according to the status.
func TestEmitFileStatusFollowsTheProgram(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		want   int
		stdout streamShape
	}{
		{
			name: "a program the front end reads and the compiler will not build",
			src:  "void main(void) { undeclared_name(); }\n",
			want: exitFailure,
		},
		{
			name:   "a program the whole pipeline builds",
			src:    "void main(void) { __ic_store(d0, Setting, 1); }\n",
			want:   exitOK,
			stdout: streamProgram,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := write(t, "stage.c", tt.src)
			var out, errOut bytes.Buffer
			cmd := &cobra.Command{
				RunE:          func(cmd *cobra.Command, _ []string) error { return emitFile(cmd, path, options{}) },
				SilenceErrors: true,
				SilenceUsage:  true,
			}
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs([]string{})

			got := execute(cmd)
			if got != tt.want {
				t.Errorf("exit status %d, want %d:\n%s", got, tt.want, errOut.String())
			}
			requireStdout(t, got, out.String(), tt.stdout)
			if tt.want != exitOK && errOut.Len() == 0 {
				t.Errorf("nothing was reported, so the status is all the caller has to go on")
			}
		})
	}
}

// refusingWriter is a stream that takes nothing at all, which is what a
// redirection onto a full filesystem is to the first write.
type refusingWriter struct{}

// refusingWriterMsg is what a refused write fails with, and has to survive into
// the message the command reports.
const refusingWriterMsg = "the stream stopped taking bytes"

func (refusingWriter) Write([]byte) (int, error) { return 0, errors.New(refusingWriterMsg) }

// truncatingWriter takes a prefix and reports that the rest did not fit, which
// is what a file size limit is to the write that crosses it.
type truncatingWriter struct{ left int }

func (w *truncatingWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.left)
	w.left -= n
	if n == len(p) {
		return n, nil
	}
	return n, errors.New(refusingWriterMsg)
}

// silentlyTruncatingWriter takes a prefix and reports that it took everything.
// That breaks [io.Writer] and [os.File] turns it into [io.ErrShortWrite], but
// the stream here is whatever a caller redirected onto and nothing about it is
// this command's to assume.
type silentlyTruncatingWriter struct{ left int }

func (w *silentlyTruncatingWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.left)
	w.left -= n
	return n, nil
}

// shortByOneWriter takes all but the last byte of every write and reports
// that it took everything — the smallest write that did not finish there
// is: a stream that took the assembly but not its closing newline leaves a
// file whose last line is unterminated.
type shortByOneWriter struct{}

func (shortByOneWriter) Write(p []byte) (int, error) { return max(len(p)-1, 0), nil }

// TestWriteLineRefusesAWriteThatDidNotFinish holds the one call this
// package writes to either stream through to landing whole. Short by a
// single byte is the case worth naming: what is missing is then the
// newline, so the assembly still reads as a program a player pastes.
func TestWriteLineRefusesAWriteThatDidNotFinish(t *testing.T) {
	const text = "move r0 1"
	const what = "the assembly " + outputStream
	tests := []struct {
		name string
		out  io.Writer
		// says is what the message has to carry of how the stream stopped. A
		// case that has to succeed leaves it empty.
		says string
	}{
		{name: "a stream that takes everything", out: &bytes.Buffer{}},
		{name: "a stream that takes nothing", out: refusingWriter{}, says: refusingWriterMsg},
		{name: "a stream that takes a prefix and stops", out: &truncatingWriter{left: 4}, says: refusingWriterMsg},
		{
			name: "a stream that takes a prefix and says it took all of it",
			out:  &silentlyTruncatingWriter{left: 4},
			says: io.ErrShortWrite.Error(),
		},
		{
			name: "a stream that takes everything but the newline",
			out:  shortByOneWriter{},
			says: io.ErrShortWrite.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writeLine(tt.out, what, text)
			if tt.says == "" {
				if err != nil {
					t.Fatalf("writing to a stream that took everything: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the write was accepted, and a stream that took part of it is what has to be reported")
			}
			if got := exitCodeFor(err); got != exitFailure {
				t.Errorf("exit status %d, want %d: %v", got, exitFailure, err)
			}
			if !strings.Contains(err.Error(), what) {
				t.Errorf("the message does not name %q, so nothing says which stream stopped: %v", what, err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("the message does not say %q, so it does not say how the stream stopped: %v", tt.says, err)
			}
		})
	}
}

// TestEveryWriteIsChecked holds every write either stream can take to
// landing whole, the ones cobra makes on its own behalf (help, version,
// completion) included, since it drops what those writes report. It drives
// execute, not the command directly, since that is where the streams are wrapped.
func TestEveryWriteIsChecked(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{name: "the help text a flag asks for", args: []string{"--help"}},
		{name: "the help text the short flag asks for", args: []string{"-h"}},
		{name: "the help text the subcommand asks for", args: []string{"help"}},
		{name: "the help text for a named subcommand", args: []string{"help", "prelude"}},
		{name: "the version", args: []string{"--version"}},
		{name: "a completion script", args: []string{"completion", "bash"}},
		{name: "a completion reply", args: []string{"__complete", ""}},
		{name: "the assembly of a program", args: []string{filepath.Join(fixtures, "thermostat.c")}},
		{name: "the list of files a prelude run wrote", args: []string{"prelude", dir}},
	}
	onto := []struct {
		name string
		out  func() io.Writer
		want int
	}{
		{name: "a stream that takes everything", out: func() io.Writer { return &bytes.Buffer{} }, want: exitOK},
		{name: "a stream that takes nothing", out: func() io.Writer { return refusingWriter{} }, want: exitFailure},
		{name: "a stream that takes everything but the last byte", out: func() io.Writer { return shortByOneWriter{} }, want: exitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, stream := range onto {
				t.Run(stream.name, func(t *testing.T) {
					var errOut bytes.Buffer
					cmd := rootCmd()
					cmd.SetOut(stream.out())
					cmd.SetErr(&errOut)
					cmd.SetArgs(tt.args)
					if got := execute(cmd); got != stream.want {
						t.Errorf("exit status %d, want %d:\n%s", got, stream.want, errOut.String())
					}
				})
			}
		})
	}
}

// flatHeaderIn is a fresh directory holding a file of the prelude header's name
// with the given contents, which is what prelude has something to say about.
func flatHeaderIn(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ic10.PreludeFileName)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return dir
}

// TestStreamThatStopsTakingBytesFailsTheRun holds a write that did not
// finish to a status that says so: a partial write to the output stream
// pastes into an IC Editor as a different, incomplete program.
func TestStreamThatStopsTakingBytesFailsTheRun(t *testing.T) {
	small := write(t, "small.c", "void main(void) { __ic_store(d0, Setting, 1); }\n")
	// Inside every limit and several times the length a truncating stream here
	// stops at, so a prefix of it is a prefix of a program that was going to be
	// emitted.
	large := write(t, "large.c", overBudgetSource(60))
	over := write(t, "over.c", overBudgetSource(140))
	rejected := write(t, "rejected.c", "void main(void) { undeclared_name(); }\n")
	dir := t.TempDir()
	// The two ways prelude has something to say about a file of the header's
	// name beside the sources, both of which it says on the error stream.
	removes := flatHeaderIn(t, ic10.Prelude)
	leaves := flatHeaderIn(t, "// a header the author of this source tree wrote\n")

	refused := func() io.Writer { return refusingWriter{} }
	tests := []struct {
		name string
		args []string
		// out and err are the streams the run is given. A nil one is an ordinary
		// buffer that takes everything.
		out  func() io.Writer
		err  func() io.Writer
		want int
		// says is what the message has to name, which is the stream that stopped,
		// and because is what it has to carry of why.
		says    string
		because string
	}{
		{
			name:    "an output stream that takes nothing",
			args:    []string{small},
			out:     refused,
			want:    exitFailure,
			says:    "the output stream",
			because: refusingWriterMsg,
		},
		{
			name:    "an output stream that takes a prefix and stops",
			args:    []string{large},
			out:     func() io.Writer { return &truncatingWriter{left: 512} },
			want:    exitFailure,
			says:    "the output stream",
			because: refusingWriterMsg,
		},
		{
			name:    "an output stream that takes a prefix and says it took all of it",
			args:    []string{large},
			out:     func() io.Writer { return &silentlyTruncatingWriter{left: 512} },
			want:    exitFailure,
			says:    "the output stream",
			because: io.ErrShortWrite.Error(),
		},
		{
			name:    "a program over a limit, which is never written to the output stream",
			args:    []string{over},
			out:     refused,
			want:    exitFailure,
			says:    "over what the in-game editor accepts",
			because: "the report names each limit it exceeds",
		},
		{
			name:    "an error stream that takes nothing",
			args:    []string{small},
			err:     refused,
			want:    exitFailure,
			says:    "the error stream",
			because: refusingWriterMsg,
		},
		{
			name:    "a program the compiler will not build, whose diagnostics reach nothing",
			args:    []string{rejected},
			err:     refused,
			want:    exitFailure,
			says:    "the error stream",
			because: refusingWriterMsg,
		},
		{
			name:    "the file list of a prelude run",
			args:    []string{"prelude", dir},
			out:     refused,
			want:    exitFailure,
			says:    "the output stream",
			because: refusingWriterMsg,
		},
		{
			name:    "a prelude run saying it removed a header of its own beside the sources",
			args:    []string{"prelude", removes},
			err:     refused,
			want:    exitFailure,
			says:    "the error stream",
			because: refusingWriterMsg,
		},
		{
			name:    "a prelude run saying it left a file of that name alone",
			args:    []string{"prelude", leaves},
			err:     refused,
			want:    exitFailure,
			says:    "the error stream",
			because: refusingWriterMsg,
		},
		{
			name: "both streams taking everything",
			args: []string{small},
			want: exitOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buffered, errBuffered bytes.Buffer
			var out io.Writer = &buffered
			if tt.out != nil {
				out = tt.out()
			}
			var errOut io.Writer = &errBuffered
			if tt.err != nil {
				errOut = tt.err()
			}
			cmd := rootCmd()
			cmd.SetOut(out)
			cmd.SetErr(errOut)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if got := exitCodeFor(err); got != tt.want {
				t.Errorf("exit status %d, want %d: %v\n%s", got, tt.want, err, errBuffered.String())
			}
			if tt.says == "" {
				if err != nil {
					t.Errorf("a run whose streams both took everything reported %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the run succeeded, and a stream that stopped taking bytes is what it has to report")
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("the message does not name %q, so nothing says which stream stopped: %v", tt.says, err)
			}
			if !strings.Contains(err.Error(), tt.because) {
				t.Errorf("the message does not say %q, so it does not say how the stream stopped: %v", tt.because, err)
			}
		})
	}
}

// TestSourceFileIsRejectedBeforeItIsRead holds the command to answering
// about a file a read of it would not finish on, rather than growing until
// something else stops it: [os.ReadFile] cannot tell such a file apart
// from an ordinary one until it is too late.
func TestSourceFileIsRejectedBeforeItIsRead(t *testing.T) {
	// A program with room to be padded out to a size the guard has an opinion
	// about, kept valid so that a case reaching the compiler compiles.
	const padded = "void main(void) { __ic_store(d0, Setting, 1); }\n"

	tests := []struct {
		name string
		// build writes the file the case runs on and returns its path.
		build func(t *testing.T) string
		want  int
		// says is what the message has to carry beyond the path.
		says string
	}{
		{
			name: "a named pipe, which a read waits on until a writer appears",
			build: func(t *testing.T) string {
				t.Helper()
				path := filepath.Join(t.TempDir(), "pipe.c")
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatalf("making a pipe at %s: %v", path, err)
				}
				return path
			},
			want: exitFailure,
			says: notRegularFile,
		},
		{
			name: "a directory, which is not a program however it is read",
			build: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			want: exitFailure,
			says: notRegularFile,
		},
		{
			name: "a regular file one byte over the limit",
			build: func(t *testing.T) string {
				t.Helper()
				// Sparse, so that measuring the guard costs no bytes: nothing
				// reads the file, which is the property under test.
				path := filepath.Join(t.TempDir(), "huge.c")
				f, err := os.Create(path)
				if err != nil {
					t.Fatalf("creating %s: %v", path, err)
				}
				if err := f.Truncate(maxSourceBytes + 1); err != nil {
					t.Fatalf("sizing %s: %v", path, err)
				}
				if err := f.Close(); err != nil {
					t.Fatalf("closing %s: %v", path, err)
				}
				return path
			},
			want: exitFailure,
			says: "over the",
		},
		{
			name: "a regular file at exactly the limit",
			build: func(t *testing.T) string {
				t.Helper()
				return write(t, "atlimit.c", padded+strings.Repeat(" ", maxSourceBytes-len(padded)))
			},
			want: exitOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.build(t)
			stdout, stderr, err := run(t, path)
			got := exitCodeFor(err)
			if got != tt.want {
				t.Errorf("exit status %d, want %d: %v\n%s", got, tt.want, err, stderr)
			}
			shape := streamNothing
			if tt.want == exitOK {
				shape = streamProgram
			}
			requireStdout(t, got, stdout, shape)
			if tt.says == "" {
				return
			}
			if err == nil {
				t.Fatalf("the file was accepted, and a read of it is what the guard exists to avoid")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the message does not name %s, which is the only thing telling this run from the one the caller meant: %v", path, err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("the message does not say %q, so it does not say what about the file the command would not take: %v", tt.says, err)
			}
		})
	}
}

// endlessReader is a source of bytes nothing about it announces: it has no
// size, no end, and no failure. It is what a character device is to a read, and
// what a regular file on a filesystem that computes its contents can be.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// refusingReader fails part way, which is the third thing a read can do.
type refusingReader struct{}

// refusingReaderMsg is what refusingReader fails with, and has to survive into
// the message the command reports.
const refusingReaderMsg = "the device stopped answering"

func (refusingReader) Read([]byte) (int, error) { return 0, errors.New(refusingReaderMsg) }

// TestReadBoundedStopsAtTheLimit covers the half of the guard a file's own
// size cannot establish: a procfs file reports zero and reads out
// kilobytes, and any file can grow after being measured, so the reported
// size is only a hint and the bytes that arrive are the bound.
func TestReadBoundedStopsAtTheLimit(t *testing.T) {
	const inside = "void main(void) { __ic_store(d0, Setting, 1); }\n"
	tests := []struct {
		name string
		read func() io.Reader
		// want is the source that has to come back, and says what the refusal
		// has to carry instead. Exactly one of the two is set.
		want string
		says string
	}{
		{
			name: "a source inside the limit",
			read: func() io.Reader { return strings.NewReader(inside) },
			want: inside,
		},
		{
			name: "a source at exactly the limit",
			read: func() io.Reader { return strings.NewReader(strings.Repeat(" ", maxSourceBytes)) },
			want: strings.Repeat(" ", maxSourceBytes),
		},
		{
			name: "a source one byte over it",
			read: func() io.Reader { return strings.NewReader(strings.Repeat(" ", maxSourceBytes+1)) },
			says: "reads past",
		},
		{
			name: "a reader with no end, which no size describes",
			read: func() io.Reader { return endlessReader{} },
			says: "reads past",
		},
		{
			// A file that read part of a program and then stopped leaves a
			// prefix that parses, so the failure has to be reported rather than
			// compiled.
			name: "a reader that fails part way",
			read: func() io.Reader { return io.MultiReader(strings.NewReader(inside), refusingReader{}) },
			says: refusingReaderMsg,
		},
	}

	const path = "source.c"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readBounded(path, tt.read())
			if tt.says == "" {
				if err != nil {
					t.Fatalf("reading a source of %d bytes: %v", len(tt.want), err)
				}
				if got != tt.want {
					t.Errorf("read %d bytes, want %d", len(got), len(tt.want))
				}
				return
			}
			if err == nil {
				t.Fatalf("the read returned %d bytes and no error, and growing until something else stops it is what the bound exists to prevent", len(got))
			}
			if got != "" {
				t.Errorf("a refused read returned %d bytes, and a prefix of a program parses as a program", len(got))
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the message does not name %s, which is the only thing telling this run from the one the caller meant: %v", path, err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("the message does not say %q, so it does not say what about the read failed: %v", tt.says, err)
			}
		})
	}
}

// TestSourceIsDescribedAndReadThroughOneHandle holds the command to
// answering about the file it reads, not about what the path meant when
// described: the name is resolved once by stat and again by the read, and
// the window between can flip it between a source and a named pipe.
func TestSourceIsDescribedAndReadThroughOneHandle(t *testing.T) {
	// A distinguishing store, so a compile of the source is recognised in the
	// assembly rather than inferred from a size.
	const marker = "987654"
	dir := t.TempDir()
	source := filepath.Join(dir, "source.c")
	pipe := filepath.Join(dir, "pipe.c")
	link := filepath.Join(dir, "src.c")

	if err := os.WriteFile(source, []byte("void main(void) { __ic_store(d0, Setting, "+marker+"); }\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", source, err)
	}
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("making a pipe at %s: %v", pipe, err)
	}
	if _, _, err := run(t, source); err != nil {
		t.Fatalf("%s is refused when it is named directly, so a run through the link says nothing about which file it read: %v", source, err)
	}
	if _, _, err := run(t, pipe); err == nil || !strings.Contains(err.Error(), notRegularFile) {
		t.Fatalf("%s is not refused for what it is when it is named directly, so nothing about reaching it through a link is a bypass: %v", pipe, err)
	}

	relink := func(target string) {
		t.Helper()
		staging := link + ".staging"
		if err := os.Symlink(target, staging); err != nil {
			t.Fatalf("linking %s: %v", staging, err)
		}
		// Renamed over rather than removed and remade, so the name always
		// resolves to one of the two and a run never sees it absent.
		if err := os.Rename(staging, link); err != nil {
			t.Fatalf("moving %s onto %s: %v", staging, link, err)
		}
	}
	relink(source)

	stop := make(chan struct{})
	flipped := make(chan int)
	go func() {
		flips := 0
		for {
			select {
			case <-stop:
				flipped <- flips
				return
			default:
			}
			relink(pipe)
			relink(source)
			flips += 2
		}
	}()

	statuses := make(map[int]int)
	compiled, bypassed := 0, 0
	for range sourceRaceAttempts {
		stdout, _, err := run(t, link)
		statuses[exitCodeFor(err)]++
		switch {
		case strings.Contains(stdout, marker):
			compiled++
		case err != nil && strings.Contains(err.Error(), notRegularFile):
		default:
			bypassed++
		}
	}
	close(stop)

	t.Logf("%d attempts against %d relinks left %v, compiling the source %d times",
		sourceRaceAttempts, <-flipped, statuses, compiled)
	if bypassed > 0 {
		t.Errorf("%d of %d runs neither compiled the source nor refused the pipe for what it is, which is a run that described one and read the other; the path was resolved once to describe it and again to read it, and the read is the one that decides",
			bypassed, sourceRaceAttempts)
	}
}

// notRegularFile is what [readSource] refuses a name that is not a plain file
// with. It is the one thing separating that refusal from a program the compiler
// would not build, since both leave the same status.
const notRegularFile = "not a regular file"

// sourceRaceAttempts is how many times TestSourceIsDescribedAndReadThroughOneHandle
// reaches for the window between describing a path and reading it. The window
// is a few instructions wide, so a count that catches it is far above the count
// that would establish anything on its own.
const sourceRaceAttempts = 400

// TestSourceAtTheCapIsCompiledRatherThanRefused holds [maxSourceBytes] to
// being the only thing that refuses a source for its size: a source at the
// cap is well inside the optimizer's own bound on module size, so nothing
// downstream refuses it first. The source is the densest form there is.
func TestSourceAtTheCapIsCompiledRatherThanRefused(t *testing.T) {
	var b strings.Builder
	b.WriteString("void main(void) {\n")
	const closing = "}\n"
	for i := 0; ; i++ {
		line := fmt.Sprintf("\t__ic_store(d0, Setting, %d);\n", i+1)
		if b.Len()+len(line)+len(closing) > maxSourceBytes {
			break
		}
		b.WriteString(line)
	}
	b.WriteString(closing)
	src := b.String()
	if len(src) > maxSourceBytes {
		t.Fatalf("the source is %d bytes and the cap is %d, so this measures the guard rather than what is past it", len(src), maxSourceBytes)
	}

	stdout, stderr, err := run(t, write(t, "atcap.c", src))
	t.Logf("a source of %d bytes, one byte under the %d byte cap, came out as %s",
		len(src), maxSourceBytes, strings.SplitN(stderr, "\n", 2)[0])
	got := exitCodeFor(err)
	if got != exitFailure {
		t.Errorf("exit status %d for a source at the cap, want %d: a status of %d would mean it never got as far as a program: %v",
			got, exitFailure, exitInternal, err)
	}
	// The status alone no longer separates a program over a limit from one the
	// compiler would not build, so what says the file was compiled the whole way
	// through is the report naming the limits the program went past.
	if !strings.Contains(stderr, overLimitDiagnostic) {
		t.Errorf("the report names no limit the program exceeded, so something refused the source before the editor's limits did:\n%s", stderr)
	}
	requireStdout(t, got, stdout, streamNothing)
}

// TestSourceLimitClearsEveryProgramHere boxes [maxSourceBytes] between the
// largest program this compiler is put through and a ceiling over the same
// figure, so the cap goes on being sized to the programs compiled here
// rather than drifting toward files.
func TestSourceLimitClearsEveryProgramHere(t *testing.T) {
	largest, from := 0, ""
	for _, dir := range []string{fixtures, refusalWitnessDir} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.c"))
		if err != nil {
			t.Fatalf("globbing %s: %v", dir, err)
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("sizing %s: %v", path, err)
			}
			if int(info.Size()) > largest {
				largest, from = int(info.Size()), path
			}
		}
	}
	// Every seed any harness in this package reaches, not the campaign's range
	// alone: the witness search runs further than the coverage corpus, and a
	// program past the cap is rejected for its size wherever it was generated.
	// Rendering is what costs here, and it is cheap next to compiling.
	for seed := uint64(1); seed <= max(generatedCoverageCorpus, refusalWitnessSearch); seed++ {
		if n := len(generateProgram(seed).render()); n > largest {
			largest, from = n, fmt.Sprintf("the generated program at seed %d", seed)
		}
	}

	t.Logf("the largest program compiled here is %s at %d bytes, which the %d byte limit stands %d times above; the gates are %d and %d times",
		from, largest, maxSourceBytes, maxSourceBytes/max(largest, 1), sourceLimitHeadroom, sourceLimitCeiling)
	if largest*sourceLimitHeadroom > maxSourceBytes {
		t.Errorf("%s is %d bytes and the limit is %d, under the %d times a program compiled here has to stay below it; a program that crosses the limit is rejected for its size rather than diagnosed",
			from, largest, maxSourceBytes, sourceLimitHeadroom)
	}
	if largest*sourceLimitCeiling < maxSourceBytes {
		t.Errorf("%s is %d bytes and the limit is %d, over the %d times the cap may stand above a program compiled here; past that the cap is sized to files rather than to programs, and what it costs to hold one was measured somewhere else",
			from, largest, maxSourceBytes, sourceLimitCeiling)
	}
}

// sourceLimitHeadroom and sourceLimitCeiling are how many times
// [maxSourceBytes] has to stand above the largest program compiled here, at
// least and at most: tripwires, not measurements, far enough apart that an
// ordinary generator change trips neither.
const (
	sourceLimitHeadroom = 32
	sourceLimitCeiling  = 96
)

// TestPanicLeavesTheInternalStatus holds a defect in the compiler to the
// status that says so: Go's runtime leaves 2 for an unrecovered panic, the
// same as a mistyped flag, so without this a compiler defect is readable as
// the caller's own mistake.
func TestPanicLeavesTheInternalStatus(t *testing.T) {
	tests := []struct {
		name string
		run  func(cmd *cobra.Command, args []string) error
		want int
	}{
		{
			name: "a stage that panics",
			run:  func(*cobra.Command, []string) error { panic("a stage reached a state it does not handle") },
			want: exitInternal,
		},
		{
			// A runtime panic rather than one the code wrote, since that is the
			// shape a defect actually takes. The nil comes back from a call so
			// that the dereference is one no analysis here can fold away, which
			// is also what keeps it from being reported as a defect of its own.
			name: "a stage that panics on a nil dereference",
			run: func(*cobra.Command, []string) error {
				return errors.New(missingDiagnostics().String())
			},
			want: exitInternal,
		},
		{
			name: "a program the compiler rejects, which is not a defect",
			run:  func(*cobra.Command, []string) error { return withStatus(exitFailure, errors.New("2 errors")) },
			want: exitFailure,
		},
		{
			name: "a run with nothing to report",
			run:  func(*cobra.Command, []string) error { return nil },
			want: exitOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			cmd := &cobra.Command{RunE: tt.run, SilenceErrors: true, SilenceUsage: true}
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs([]string{})

			if got := execute(cmd); got != tt.want {
				t.Errorf("exit status %d, want %d:\n%s", got, tt.want, errOut.String())
			}
			if out.Len() > 0 {
				t.Errorf("the output stream holds %q, and no case here emits a program", out.String())
			}
			if tt.want != exitOK && errOut.Len() == 0 {
				t.Errorf("nothing was reported, so the status is all the caller has to go on")
			}
		})
	}
}

// missingDiagnostics is the stage that returned nothing where the caller reads
// something, which is the shape of the defect exitInternal is for.
//
//go:noinline
func missingDiagnostics() *source.DiagnosticList { return nil }

// TestPanicReportsWhereItCameFrom holds the internal-error report to carrying a
// stack. The status says a defect in the compiler and nothing else does, so the
// report is the only thing that says which one.
func TestPanicReportsWhereItCameFrom(t *testing.T) {
	const marker = "a stage reached a state it does not handle"
	var errOut bytes.Buffer
	cmd := &cobra.Command{
		RunE:          func(*cobra.Command, []string) error { panic(marker) },
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{})

	if got := execute(cmd); got != exitInternal {
		t.Fatalf("exit status %d, want %d", got, exitInternal)
	}
	report := errOut.String()
	if !strings.Contains(report, marker) {
		t.Errorf("the report does not carry what the panic said:\n%s", report)
	}
	if !strings.Contains(report, "cmd/ic11c") {
		t.Errorf("the report carries no stack through this package, so it names no defect to go and look at:\n%s", report)
	}
}
