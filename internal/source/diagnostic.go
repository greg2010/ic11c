package source

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Severity says whether a diagnostic rejects the program or only informs about
// it. The zero value is [Error], so a diagnostic built without naming one
// rejects.
type Severity uint8

const (
	// Error rejects the program. Nothing is emitted.
	Error Severity = iota
	// Warning describes a construct the compiler accepts and emits. It is for a
	// program that means what it says and would be better written differently —
	// the machine accepting something the game has since retired, say.
	Warning
)

var severityNames = [...]string{Error: "error", Warning: "warning"}

func (s Severity) String() string { return EnumName(severityNames[:], int(s), "Severity") }

// Diagnostic is one construct the compiler has something to say about, located
// in the source.
type Diagnostic struct {
	Pos      Position
	Severity Severity
	Msg      string
	// Overflow marks the note closing a list that was cut short at a cap. It
	// describes a limit the compiler imposes on itself rather than anything
	// wrong with the program, so the counts that decide rejection skip it. See
	// [DiagnosticList.Overflow].
	Overflow bool
}

// Error renders the diagnostic as "position: message", with the severity named
// only when it is not an error. An error needs no label: it is what a compiler
// prints by default, and anything else has to say so.
func (d Diagnostic) Error() string {
	if d.Severity == Error {
		return d.Pos.String() + ": " + d.Msg
	}
	return d.Pos.String() + ": " + d.Severity.String() + ": " + d.Msg
}

// DiagnosticList accumulates diagnostics so that a phase can report every
// problem it found rather than only the first. The zero value is an empty list
// ready for use.
type DiagnosticList []Diagnostic

// Addf records an error at pos.
func (l *DiagnosticList) Addf(pos Position, format string, args ...any) {
	*l = append(*l, Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// Warnf records a warning at pos. The program is still compiled and emitted;
// only [DiagnosticList.Err] and [DiagnosticList.HasErrors] decide that, and
// neither counts a warning.
func (l *DiagnosticList) Warnf(pos Position, format string, args ...any) {
	*l = append(*l, Diagnostic{Pos: pos, Severity: Warning, Msg: fmt.Sprintf(format, args...)})
}

// Overflow closes a list cut short at a cap with the note saying so, at
// pos: where the first problem withheld sits. Call it after
// [DiagnosticList.Sort]. The note is excluded from [DiagnosticList.Errors]
// and [DiagnosticList.HasErrors].
func (l *DiagnosticList) Overflow(pos Position, severity Severity) {
	*l = append(*l, Diagnostic{
		Pos:      pos,
		Severity: severity,
		Msg:      "too many " + severity.String() + "s",
		Overflow: true,
	})
}

// HasErrors reports whether any diagnostic rejects the program. The note
// closing a cut-short list is not one, whatever severity it carries.
func (l DiagnosticList) HasErrors() bool {
	for _, d := range l {
		if d.Severity == Error && !d.Overflow {
			return true
		}
	}
	return false
}

// Errors counts the diagnostics that reject the program, which is the number a
// report may print. The note closing a cut-short list is not counted; see
// [DiagnosticList.Overflow].
func (l DiagnosticList) Errors() int {
	n := 0
	for _, d := range l {
		if d.Severity == Error && !d.Overflow {
			n++
		}
	}
	return n
}

// Sort orders the list by source position, so diagnostics from a phase that
// runs after lexing interleave with lexical ones in reading order.
func (l *DiagnosticList) Sort() {
	slices.SortStableFunc(*l, func(a, b Diagnostic) int { return a.Pos.Compare(b.Pos) })
}

// Error renders the first diagnostic, noting how many others follow. It is
// defined so a DiagnosticList can be returned as an error; callers wanting
// every message should range over the list.
func (l DiagnosticList) Error() string {
	switch len(l) {
	case 0:
		return "no errors"
	case 1:
		return l[0].Error()
	default:
		return l[0].Error() + " (and " + strconv.Itoa(len(l)-1) + " more)"
	}
}

// Plural renders a count and its noun, adding an s for any count but one.
//
// The noun must have a regular plural; nothing the compiler counts does not.
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Err returns l as an error, or nil when nothing in it rejects the program. A
// list holding only warnings is not an error: the stage that produced them
// carries on and the caller emits.
func (l DiagnosticList) Err() error {
	if !l.HasErrors() {
		return nil
	}
	return l
}

// DiagnosticsIn recovers a list returned as an error by [DiagnosticList.Err],
// however deeply wrapped, which is how a caller separates a stage's report
// against the program from a failure of the stage itself. A diagnostic names the
// line to act on, so a caller finding one passes it on rather than wraps it.
func DiagnosticsIn(err error) (DiagnosticList, bool) {
	return errors.AsType[DiagnosticList](err)
}

// String renders every diagnostic on its own line.
func (l DiagnosticList) String() string {
	var b strings.Builder
	for i, d := range l {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(d.Error())
	}
	return b.String()
}
