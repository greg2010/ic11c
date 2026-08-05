package main

import "errors"

// The exit statuses the command leaves.
//
// exitOK: the output stream holds the complete assembly compiled (which can
// be empty text). exitFailure: every way a run ends with no program to keep
// (unreadable source, a refused program, one over an editor limit, or a
// stream write that did not finish). exitUsage: the command line itself.
// exitInternal: EX_SOFTWARE from sysexits.h, chosen for its distance from
// the other three so a compiler defect is never mistaken for the source's fault.
const (
	exitOK       = 0
	exitFailure  = 1
	exitUsage    = 2
	exitInternal = 70
)

// statusError fixes the exit status a failure leaves. Every error the compiler
// itself produces carries one; an error arriving without one came out of
// cobra's argument handling, which is a usage error.
type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string { return e.err.Error() }

func (e *statusError) Unwrap() error { return e.err }

// withStatus fixes the exit status err leaves.
func withStatus(code int, err error) error {
	return &statusError{code: code, err: err}
}

// exitCodeFor reports the status a run ending in err leaves the shell.
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	if status, ok := errors.AsType[*statusError](err); ok {
		return status.code
	}
	return exitUsage
}
