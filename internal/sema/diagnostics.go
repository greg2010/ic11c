package sema

import (
	"fmt"

	"github.com/greg2010/ic11c/internal/source"
)

// maxErrors and maxWarnings cap how many problems of each severity one
// analysis reports; past either point the output is noise. They are counted
// apart, not against one sum: a program with only warnings still compiles,
// so a shared budget would let warnings crowd out the errors that decide that.
const (
	maxErrors   = 64
	maxWarnings = 64
)

// budget is one severity's allowance: how many messages of it have been issued,
// how many may be, and where the first one dropped sat.
type budget struct {
	issued int
	max    int
	// stopped is the position of the first message the budget refused, which is
	// what the note closing the list is placed at.
	stopped   source.Position
	truncated bool
}

func (c *checker) errorf(pos source.Position, format string, args ...any) {
	if msg, ok := c.record(pos, source.Error, format, args...); ok {
		c.diags.Addf(pos, "%s", msg)
	}
}

// warnf records a diagnostic that does not reject the program. An operand
// checked twice reaches here twice, so it shares errorf's suppression of an
// identical message at an identical position.
func (c *checker) warnf(pos source.Position, format string, args ...any) {
	if msg, ok := c.record(pos, source.Warning, format, args...); ok {
		c.diags.Warnf(pos, "%s", msg)
	}
}

// record applies the two limits every diagnostic is subject to: one message per
// position, and the cap on its own severity past which the output is noise. It
// reports whether the message should be added.
func (c *checker) record(pos source.Position, severity source.Severity, format string, args ...any) (string, bool) {
	msg := fmt.Sprintf(format, args...)
	key := pos.String() + ": " + msg
	if c.reported[key] {
		return "", false
	}
	c.reported[key] = true
	b := c.budget(severity)
	if b.issued >= b.max {
		if !b.truncated {
			b.truncated = true
			b.stopped = pos
		}
		return "", false
	}
	b.issued++
	return msg, true
}

// budget is the allowance one severity draws on.
func (c *checker) budget(severity source.Severity) *budget {
	if severity == source.Warning {
		return &c.warnings
	}
	return &c.errors
}

// closeLists notes each severity that ran out of budget, at the position of
// the first message it refused. It runs once the list is sorted, not when
// the budget ran out: sorting a note in with the rest would sink it among
// the messages it claims to follow.
func (c *checker) closeLists() {
	for _, severity := range [...]source.Severity{source.Error, source.Warning} {
		if b := c.budget(severity); b.truncated {
			c.diags.Overflow(b.stopped, severity)
		}
	}
}
