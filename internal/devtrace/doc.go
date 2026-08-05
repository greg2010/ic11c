// Package devtrace records what a program does to the devices around it, so
// that two runs of the same program can be compared on the only surface a chip
// exposes.
//
// It is test support: [Run] and [RunNative] take a *testing.T and fail through it.
//
// A [Trace] holds only the device writes a run made and how it ended: not
// registers, addresses, or instruction counts, which two correct builds of one
// program are free to differ on. [Run] and [RunNative] align two builds by
// segment rather than by tick — see [Stimulus] — and [Diff] states how far two
// traces can be held to each other depending on what produced them.
package devtrace
