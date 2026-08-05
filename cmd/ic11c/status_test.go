package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// shippedCommand runs the command as it ships, over args.
func shippedCommand(args ...string) func(t *testing.T) (*cobra.Command, []string) {
	return func(*testing.T) (*cobra.Command, []string) { return rootCmd(), args }
}

// shippedCommandOver writes src to a file of its own and runs the command as it
// ships over it, after any flags the case names.
func shippedCommandOver(src string, flags ...string) func(t *testing.T) (*cobra.Command, []string) {
	return func(t *testing.T) (*cobra.Command, []string) {
		t.Helper()
		return rootCmd(), append(slices.Clone(flags), write(t, "status.c", src))
	}
}

// TestBothStreamsFollowTheStatus reads both streams of one run together,
// since a case reading only one cannot see whether a failing run explained
// itself or a program still went out. It drives execute, not the command
// directly, since that is where all four statuses are reachable.
func TestBothStreamsFollowTheStatus(t *testing.T) {
	thermostat := filepath.Join(fixtures, "thermostat.c")
	tests := []struct {
		name string
		// build returns the command the case runs and the arguments it runs
		// with, so that a case wanting a source file writes one first.
		build func(t *testing.T) (*cobra.Command, []string)
		want  int
		// stdout is what the output stream has to hold. The zero value is
		// streamNothing, which is what everything but a compiled program leaves.
		stdout streamShape
		// says is what the error stream has to carry. Under a failing status it
		// is the whole of what the caller is left with.
		says string
	}{
		{
			name:   "a program compiled and inside every limit",
			build:  shippedCommand(thermostat),
			want:   exitOK,
			stdout: streamProgram,
			says:   "program: ",
		},
		{
			name:   "a program whose functions hold no instruction",
			build:  shippedCommandOver("void main(void) { }\n"),
			want:   exitOK,
			stdout: streamEmptyProgram,
			says:   "0 of 128 lines",
		},
		{
			name:  "a program over the 128 line limit",
			build: shippedCommandOver(overBudgetSource(140)),
			want:  exitFailure,
			says:  overLimitDiagnostic,
		},
		{
			name:  "a program over the 4096 byte budget",
			build: shippedCommandOver(byteBudgetSource(63)),
			want:  exitFailure,
			says:  overLimitDiagnostic,
		},
		{
			name:  "a program over the 90 character line limit",
			build: shippedCommandOver(lineLengthSource),
			want:  exitFailure,
			says:  overLimitDiagnostic,
		},
		{
			// A flag changes what is emitted and what the report describes, and
			// changes nothing about what the stream holds under which status.
			// Which limit each mode reaches is TestOverBudgetProgramFails.
			name:  "a program over a limit under a flag that changes what is emitted",
			build: shippedCommandOver(overBudgetSource(140), "--numeric"),
			want:  exitFailure,
			says:  overLimitDiagnostic,
		},
		{
			name:  "a program that both warns and is over a limit",
			build: shippedCommandOver(warnedOverBudgetSource(140)),
			want:  exitFailure,
			says:  overLimitDiagnostic,
		},
		{
			name:  "a program naming something that was never declared",
			build: shippedCommandOver("void main(void) { undeclared_name(); }\n"),
			want:  exitFailure,
			says:  "undeclared_name",
		},
		{
			name: "a source file that does not exist",
			build: func(t *testing.T) (*cobra.Command, []string) {
				t.Helper()
				return rootCmd(), []string{filepath.Join(t.TempDir(), "absent.c")}
			},
			want: exitFailure,
			says: "absent.c",
		},
		{
			name: "a directory given where a source file goes",
			build: func(t *testing.T) (*cobra.Command, []string) {
				t.Helper()
				return rootCmd(), []string{t.TempDir()}
			},
			want: exitFailure,
			says: notRegularFile,
		},
		{
			name:  "a flag the command does not have",
			build: shippedCommand("--no-such-flag", thermostat),
			want:  exitUsage,
			says:  "unknown flag",
		},
		{
			name:  "no source file at all",
			build: shippedCommand(),
			want:  exitUsage,
			says:  "exactly one source file",
		},
		{
			name:  "two source files",
			build: shippedCommand(thermostat, filepath.Join(fixtures, "bits.c")),
			want:  exitUsage,
			says:  "exactly one source file",
		},
		{
			name: "a stage that panics",
			build: func(*testing.T) (*cobra.Command, []string) {
				return &cobra.Command{
					RunE:          func(*cobra.Command, []string) error { panic("a stage reached a state it does not handle") },
					SilenceErrors: true,
					SilenceUsage:  true,
				}, []string{}
			},
			want: exitInternal,
			says: "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := tt.build(t)
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(args)

			got := execute(cmd)
			if got != tt.want {
				t.Errorf("exit status %d, want %d:\n%s", got, tt.want, errOut.String())
			}
			// Stated here as well as through requireStdout, so that the
			// invariant holds whatever shape a case claims for its own stream.
			if got != exitOK && out.Len() > 0 {
				t.Errorf("exit status %d left %q on the output stream, and a redirection catches that as a program the caller can paste; only exit status %d says there is one",
					got, out.String(), exitOK)
			}
			requireStdout(t, got, out.String(), tt.stdout)
			if !strings.Contains(errOut.String(), tt.says) {
				t.Errorf("the error stream does not say %q, which is what this run has to leave the caller:\n%s", tt.says, errOut.String())
			}
		})
	}
}
