// Command ic11c compiles MicroC source to Stationeers IC10 assembly.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/greg2010/ic11c/internal/source"
	"github.com/spf13/cobra"
)

// version is set at build time by the release workflow's -X main.version=<tag>.
var version = "dev"

func main() { os.Exit(execute(rootCmd())) }

// execute runs cmd and reports the exit status. A recovered panic reports
// exitInternal rather than Go's default of 2, which would be indistinguishable
// from exitUsage; streams are wrapped in [checkedWriter] so a write that does
// not finish is caught whether this package or cobra made it.
func execute(cmd *cobra.Command) (code int) {
	streams := guardStreams(cmd)
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// The write's own result is not checked: a broken stream must not
		// change the internal-error status it is reporting.
		fmt.Fprintf(cmd.ErrOrStderr(), "ic11c: internal error: %v\n\n%s", r, debug.Stack())
		code = exitInternal
	}()

	err := cmd.Execute()
	if err == nil {
		// Catches a write cobra made on its own behalf (help, usage,
		// completion) that nothing else would report.
		err = streams.failure()
	}
	if err == nil {
		return exitOK
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "ic11c: %v\n", err)
	code = exitCodeFor(err)
	// An error with no status of its own defaults to exitUsage, which is a
	// guess (cobra's completion command returns bare errors this way). A
	// stream write that did not land is not a guess, so it overrides that.
	if code == exitUsage && streams.failure() != nil {
		code = exitFailure
	}
	return code
}

// checkedWriter passes writes through to a command stream and keeps the
// first that did not finish. Cobra writes to these streams on its own behalf
// (help, usage, version, completion) and drops the result; wrapping the
// stream catches those writes too.
type checkedWriter struct {
	stream string
	w      io.Writer
	err    error
}

func (c *checkedWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if c.err == nil {
		// Only the first failure is recorded: it is the one that says what
		// happened.
		c.err = writeFailure(c.stream, n, len(p), err)
	}
	return n, err
}

// streams are the command's two output streams, each held to finishing what is
// written to it.
type streams struct{ out, err *checkedWriter }

// guardStreams routes cmd's streams through [checkedWriter], so cobra's own
// writes are checked the same as this package's.
func guardStreams(cmd *cobra.Command) streams {
	guarded := streams{
		out: &checkedWriter{stream: outputStream, w: cmd.OutOrStdout()},
		err: &checkedWriter{stream: errorStream, w: cmd.ErrOrStderr()},
	}
	cmd.SetOut(guarded.out)
	cmd.SetErr(guarded.err)
	return guarded
}

// failure reports a write to either stream that did not finish, or nothing.
// The output stream is checked first: a partial write there pastes into an
// IC Editor as a different, incomplete program.
func (s streams) failure() error {
	for _, stream := range []*checkedWriter{s.out, s.err} {
		if stream.err != nil {
			return withStatus(exitFailure, stream.err)
		}
	}
	return nil
}

// maxSourceBytes bounds the read, not the language: an unbounded read blocks
// forever on a character device or a named pipe with no writer, and a large
// regular file costs several times its size to hold before a line is parsed.
// See TestSourceAtTheCapIsCompiledRatherThanRefused.
const maxSourceBytes = 1 << 20

// sourceLimitReason is the shared explanation for the two ways a file can be
// found to exceed maxSourceBytes: stat before reading, and reading past it.
const sourceLimitReason = "the largest program this compiler is built for is a small fraction of the limit, and a file this size costs more to hold than any program in it could cost to compile"

// readSource reads path as a source file, bounding the read rather than
// trusting the name. The open is non-blocking, since an ordinary open of a
// named pipe with no writer blocks forever, and the file is examined through
// the open handle rather than a prior stat to avoid a race between the two.
func readSource(path string) (src string, err error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() {
		// A close failure on a read-only handle doesn't taint the bytes
		// already read; join rather than replace.
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing %s: %w", path, closeErr))
		}
	}()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("reading %s: not a regular file, and a read of a device, a pipe or a directory need never answer with a program", path)
	}
	if info.Size() > maxSourceBytes {
		return "", fmt.Errorf("reading %s: %d bytes, over the %s a source file may be; %s",
			path, info.Size(), source.Plural(maxSourceBytes, "byte"), sourceLimitReason)
	}
	return readBounded(path, f)
}

// readBounded reads a source out of r, refusing one with more to give than
// maxSourceBytes. The limit is enforced on bytes actually read, not the size
// Stat reports: procfs files report size zero, and a file can grow after
// being measured.
func readBounded(path string, r io.Reader) (string, error) {
	src, err := io.ReadAll(io.LimitReader(r, maxSourceBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if len(src) > maxSourceBytes {
		return "", fmt.Errorf("reading %s: reads past the %s a source file may be, whatever size it reports; %s",
			path, source.Plural(maxSourceBytes, "byte"), sourceLimitReason)
	}
	return string(src), nil
}

// The two streams a message about a failed write names. A caller redirects them
// separately, so which one stopped taking bytes is most of what such a message
// has to say.
const (
	outputStream = "to the output stream"
	errorStream  = "to the error stream"
)

// writeFailure reports a write that did not finish, or nothing when it did.
// what names the destination the resulting error is reported against. The
// byte count is checked too: a writer that takes fewer bytes than given and
// returns no error violates [io.Writer].
func writeFailure(what string, n, want int, err error) error {
	switch {
	case err != nil:
		return fmt.Errorf("writing %s: %w", what, err)
	case n != want:
		return fmt.Errorf("writing %s: %d of %d bytes: %w", what, n, want, io.ErrShortWrite)
	}
	return nil
}

// writeLine writes text and a newline to a command stream, reporting a write
// that did not finish. A partial write to the output stream pastes into an
// IC Editor as a different, incomplete program, so it must not leave exitOK.
// The check applies whether or not [guardStreams] wrapped the stream.
func writeLine(w io.Writer, what, text string) error {
	n, err := fmt.Fprintln(w, text)
	if failed := writeFailure(what, n, len(text)+1, err); failed != nil {
		return withStatus(exitFailure, failed)
	}
	return nil
}

// rootCmd builds the compiler command. Its streams default to the process
// streams and are redirectable with SetOut and SetErr, so a test can drive it
// without touching process globals.
func rootCmd() *cobra.Command {
	var opts options
	root := &cobra.Command{
		Use:   "ic11c <source>",
		Short: "Compile MicroC source to Stationeers IC10 assembly",
		Long: `ic11c compiles a MicroC source file to Stationeers IC10 assembly.
The assembly is written to stdout for pasting into an IC Editor.

The prelude subcommand writes the two files a C editor needs to read MicroC.`,
		Version: version,
		// main reports the error with the binary's own prefix; without this
		// cobra prints it a second time.
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("expected exactly one source file")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return emitFile(cmd, args[0], opts)
		},
	}
	root.Flags().BoolVar(&opts.readable, "readable", false,
		"name the block each line opens and the block each branch goes to, in a comment after the instruction; the chip cuts a line at its first '#', so the program is the shipped one on the same lines and only its byte count and line widths differ; a comment carried past the 90 character width is cut on paste and does not fail the compile")
	root.Flags().BoolVar(&opts.numeric, "numeric", false,
		"emit the integer behind every logic type, slot type, batch mode and reagent mode instead of its name; identical to the chip and the smallest form, for a program that has run out of bytes")
	root.Flags().BoolVar(&opts.skipOptimizer, "no-optimize", false,
		"skip the optimizer, emitting what IR generation produced; several times larger, and what an optimized program is compared against when it looks wrong")
	// Cobra's default renders "ic11c version <v>"; the bare version string is
	// what -version has always printed.
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetHelpCommand(helpCmd())
	root.AddCommand(preludeCmd())
	return root
}

// helpCmd replaces cobra's own help subcommand, which calls [os.Exit](1) if
// its usage message for an unknown topic fails to write. This one reports
// through the command like every other failure, so a broken stream leaves a
// consistent status.
func helpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long:  "help prints what ic11c or one of its subcommands takes and does.",
		RunE: func(cmd *cobra.Command, args []string) error {
			about, rest, err := cmd.Root().Find(args)
			if err != nil {
				return err
			}
			if len(rest) > 0 {
				return fmt.Errorf("no command named %s to give help about", strings.Join(rest, " "))
			}
			// Cobra normally adds these as it runs a command; add them here
			// since about is being described, not run.
			about.InitDefaultHelpFlag()
			about.InitDefaultVersionFlag()
			return about.Help()
		},
	}
}

// emitFile compiles one file, writing the assembly to the command's output
// stream and diagnostics plus the size report to its error stream. The
// assembly is written last, only once the run is about to succeed: a program
// the in-game editor will not take must never be pasteable from a failing run.
func emitFile(cmd *cobra.Command, path string, opts options) error {
	src, err := readSource(path)
	if err != nil {
		return withStatus(exitFailure, err)
	}

	output, diags, err := compile(cmd.Context(), path, src, opts)
	for _, diag := range diags {
		if writeErr := writeLine(cmd.ErrOrStderr(), "a diagnostic "+errorStream, diag.Error()); writeErr != nil {
			return writeErr
		}
	}
	// A stage that could not run at all arrives as an error (a compiler
	// defect); a program the stage refuses arrives as diagnostics.
	if err != nil {
		return withStatus(exitInternal, err)
	}
	if errs := diags.Errors(); errs > 0 {
		return withStatus(exitFailure, fmt.Errorf("%s: %s", path, source.Plural(errs, "error")))
	}

	if err := writeLine(cmd.ErrOrStderr(), "the size report "+errorStream, output.Report.String()); err != nil {
		return err
	}
	if len(output.Report.Violations) > 0 {
		return withStatus(exitFailure,
			fmt.Errorf("%s: the program is over what the in-game editor accepts; the report names each limit it exceeds", path))
	}
	// Empty output emits nothing: a bare newline on the paste target is a
	// line the chip charges for.
	if output.Text == "" {
		return nil
	}
	return writeLine(cmd.OutOrStdout(), "the assembly "+outputStream, output.Text)
}
