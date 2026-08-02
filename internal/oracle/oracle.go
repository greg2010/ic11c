// Package oracle drives external IC10 emulators as differential-testing references.
//
// An emulator runs as a long-lived subprocess speaking JSON Lines: a banner object on startup,
// then one request object per line on stdin and one response object per line on stdout, strictly
// in order. Process startup dominates per-program cost — ic10emu builds a 54k-entry prefab map
// before it can run anything — so a single process is reused for a whole test binary.
//
// The 512-slot memory array travels sparsely in both directions, as (address, bits) pairs for the
// slots that are not +0. A dense array would dominate the cost of every exchange.
//
// Doubles cross the wire as IEEE-754 bit patterns written in decimal, as strings. JSON cannot
// represent NaN or infinity, decimal round-tripping loses NaN payloads and the sign of zero, and
// a JSON number wide enough to hold an arbitrary bit pattern does not survive a JavaScript
// JSON.parse. Bit-exact comparison needs all of that preserved.
//
// No emulator here is authoritative. Both disagree with the Stationeers depot manifest named by
// ic10.ManifestID in documented ways; see the divergence registry in this package before treating
// a mismatch as a bug.
package oracle

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/greg2010/ic11c/internal/ic10"
)

// RegisterCount is the register file the wire protocol carries, which is the chip's own: r0-r15
// plus sp (r16) and ra (r17). State and internal/difftest type their register arrays by it, so
// the equality is what makes a harness disagreeing about the file size a compile error rather
// than a silent truncation.
const RegisterCount = ic10.NumRegisters

// protocolVersion is the banner version this client understands.
const protocolVersion = 1

// DefaultMaxInstructions is the budget Run applies when the caller passes zero.
const DefaultMaxInstructions = 100_000

// bannerTimeout bounds the wait for a server's startup banner so a wrong or wedged executable
// surfaces as an error instead of a hang.
const bannerTimeout = 30 * time.Second

const (
	// defaultServerTimeout bounds one program on the server when the caller sets no deadline.
	defaultServerTimeout = 5 * time.Second
	// serverTimeoutMargin leaves the server room to answer before the caller gives up.
	serverTimeoutMargin = 250 * time.Millisecond
)

// Harness names an emulator implementation.
type Harness string

const (
	// IC10Emu is the patched Ryex/ic10emu build, the primary reference.
	IC10Emu Harness = "ic10emu"
	// NPM is the ic10 npm package, a secondary cross-check. It is more accurate than ic10emu on
	// constants and implements more instructions, but has no tick model and only five error
	// codes, so it cannot serve as the primary reference.
	NPM Harness = "ic10npm"
)

// State is one end of a differential comparison: the machine state before or after a run.
type State struct {
	Registers [RegisterCount]float64
	Stack     [ic10.NumMemorySlots]float64
}

// Result is an emulator's verdict on a program.
type Result struct {
	Final State
	// InstructionPointer is the zero-based source line index the chip stopped on.
	InstructionPointer uint32
	// Status is one of: start, running, ended, error, yield, sleep, fire, budget_exhausted,
	// tick_budget_exhausted.
	Status string
	// ErrorType is a stable variant path such as "MemoryError.StackOverflow", empty on success.
	// The two harnesses use disjoint vocabularies; see the divergence registry.
	ErrorType string
	// ErrorLine is the zero-based source line that faulted, meaningful only when ErrorType is set.
	ErrorLine uint32
	ErrorMsg  string
	// CompileErrors are faults raised while parsing, before any instruction ran. ic10emu still
	// executes the program, substituting a nop for each unparsable line.
	CompileErrors []string
	// Instructions retired across the whole run.
	Instructions uint64
	// Ticks entered, including a partial last one. Always zero for the npm harness.
	Ticks uint64
}

// Info describes the server on the far end of the pipe, from its startup banner.
type Info struct {
	// Oracle is the server's self-reported name, matching a Harness value.
	Oracle Harness `json:"oracle"`
	// Protocol is the wire version. This client requires protocolVersion.
	Protocol uint32 `json:"protocol"`
	// IC10EmuCommit is the upstream revision the ic10emu server was built from, empty elsewhere.
	IC10EmuCommit string `json:"ic10emu_commit"`
	// Patches are the patch file names applied to that revision.
	Patches []string `json:"patches"`
	// IC10Version is the npm package version, empty elsewhere.
	IC10Version         string `json:"ic10_version"`
	RegisterCount       int    `json:"register_count"`
	StackSlots          int    `json:"stack_slots"`
	InstructionsPerTick uint64 `json:"instructions_per_tick"`
}

type request struct {
	Code            string       `json:"code"`
	Registers       []string     `json:"registers,omitempty"`
	Stack           []stackEntry `json:"stack,omitempty"`
	MaxInstructions uint64       `json:"max_instructions,omitempty"`
	MaxTicks        uint64       `json:"max_ticks,omitempty"`
	// TimeoutMS bounds a single program server-side. Only the npm harness honours it, where a
	// `sleep` blocks inside one step and would otherwise wedge the shared server.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// stackEntry is one memory slot, encoded on the wire as [address, bits]. Memory is sparse in
// both directions: only slots that are not +0 are sent, which keeps a 512-element array off
// every exchange.
type stackEntry struct {
	Address int
	Bits    string
}

func (e stackEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{e.Address, e.Bits})
}

func (e *stackEntry) UnmarshalJSON(data []byte) error {
	var pair [2]json.RawMessage
	if err := json.Unmarshal(data, &pair); err != nil {
		return fmt.Errorf("stack entry: %w", err)
	}
	if err := json.Unmarshal(pair[0], &e.Address); err != nil {
		return fmt.Errorf("stack entry address: %w", err)
	}
	if err := json.Unmarshal(pair[1], &e.Bits); err != nil {
		return fmt.Errorf("stack entry bits: %w", err)
	}
	return nil
}

type response struct {
	OK            bool         `json:"ok"`
	Registers     []string     `json:"registers"`
	Stack         []stackEntry `json:"stack"`
	IP            uint32       `json:"ip"`
	State         string       `json:"state"`
	ErrorType     string       `json:"error_type"`
	ErrorLine     uint32       `json:"error_line"`
	ErrorMsg      string       `json:"error_msg"`
	CompileErrors []string     `json:"compile_errors"`
	Instructions  uint64       `json:"instructions"`
	Ticks         uint64       `json:"ticks"`
}

// Client is a handle on a running emulator subprocess. It is safe for concurrent use; calls are
// serialized because the wire protocol is a single ordered request/response stream.
type Client struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *tailBuffer
	info   Info
	killed bool
	// broken records the failure that desynchronized the stream. Once set, the stream can no
	// longer be trusted to line responses up with requests, so every later call fails with it.
	broken error
}

// Start launches the given harness and reads its banner.
//
// ctx bounds the subprocess lifetime, not a single call: cancelling it kills the server. Pass a
// context that lives as long as the client. The caller should Close the returned Client, though
// the server also exits on its own when the parent process closes the pipe.
//
// Start returns an error wrapping ErrNotBuilt when the harness has not been built on this
// machine; callers under test should route that to a skip rather than a failure.
func Start(ctx context.Context, h Harness) (*Client, error) {
	argv, err := Locate(h)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stdin pipe: %w", h, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stdout pipe: %w", h, err)
	}
	stderr := &tailBuffer{limit: 8192}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: start %s: %w", h, argv[0], err)
	}

	c := &Client{
		cmd:   cmd,
		stdin: stdin,
		// One response is one line, and a fully populated memory array or a long list of compile
		// errors runs well past the default 4 KiB, which would split it.
		stdout: bufio.NewReaderSize(stdout, 1<<20),
		stderr: stderr,
	}
	if readerDone, err := c.readBanner(ctx, h); err != nil {
		return nil, errors.Join(err, c.kill(readerDone))
	}
	return c, nil
}

// readBanner reads and validates the server's startup banner.
//
// The returned channel is closed once the background read has finished, whether or not a banner
// arrived. It is what kill needs to order the reap after the read; see kill.
func (c *Client) readBanner(ctx context.Context, h Harness) (<-chan struct{}, error) {
	type outcome struct {
		line []byte
		err  error
	}
	done := make(chan outcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		line, err := c.stdout.ReadBytes('\n')
		done <- outcome{line, err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			return finished, fmt.Errorf("%s: read banner: %w%s", h, got.err, c.stderr.suffix())
		}
		if err := json.Unmarshal(got.line, &c.info); err != nil {
			return finished, fmt.Errorf("%s: parse banner: %w", h, err)
		}
	case <-ctx.Done():
		return finished, fmt.Errorf("%s: waiting for banner: %w", h, ctx.Err())
	case <-time.After(bannerTimeout):
		return finished, fmt.Errorf("%s: no banner within %s; is %q an oracle server?",
			h, bannerTimeout, c.cmd.Path)
	}

	switch {
	case c.info.Oracle != h:
		return finished, fmt.Errorf("%s: server identifies as %q", h, c.info.Oracle)
	case c.info.Protocol != protocolVersion:
		return finished, fmt.Errorf("%s: server speaks protocol %d, this client speaks %d",
			h, c.info.Protocol, protocolVersion)
	case c.info.RegisterCount != RegisterCount || c.info.StackSlots != ic10.NumMemorySlots:
		return finished, fmt.Errorf("%s: server has %d registers and %d stack slots, this client assumes %d and %d",
			h, c.info.RegisterCount, c.info.StackSlots, RegisterCount, ic10.NumMemorySlots)
	}
	return finished, nil
}

// Info returns the banner the server sent at startup.
func (c *Client) Info() Info { return c.info }

// Close shuts the subprocess down by closing its stdin and waiting for it to exit. It is a no-op
// on a client that was already killed, so it stays safe to defer.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killed {
		return nil
	}
	if err := c.stdin.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return fmt.Errorf("close stdin: %w", err)
	}
	if err := c.cmd.Wait(); err != nil {
		if c.broken != nil {
			return fmt.Errorf("wait after %v: %w%s", c.broken, err, c.stderr.suffix())
		}
		return fmt.Errorf("wait: %w%s", err, c.stderr.suffix())
	}
	return nil
}

// kill terminates the server and reaps it.
//
// readerDone is closed by the goroutine reading stdout. os/exec requires every read on a
// StdoutPipe to have finished before Wait, which closes the pipe under the reader; killing the
// process is what unblocks a read still in flight. Hence the order: kill, drain, reap.
//
// The reap reports the signal just sent, as an *exec.ExitError, and that is not a failure worth
// telling the caller about. Anything else is: Wait also returns the error from copying the
// subprocess's streams, which means the stderr tail this package attaches to every other
// diagnostic is incomplete.
func (c *Client) kill(readerDone <-chan struct{}) error {
	c.killed = true
	if c.cmd.Process == nil {
		return nil
	}
	if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill %s: %w", c.cmd.Path, err)
	}
	<-readerDone
	var exited *exec.ExitError
	if err := c.cmd.Wait(); err != nil && !errors.As(err, &exited) {
		return fmt.Errorf("reap %s after kill: %w%s", c.cmd.Path, err, c.stderr.suffix())
	}
	return nil
}

// Run executes source against initial and returns the emulator's final state.
//
// maxInstructions bounds non-terminating programs; the result reports status "budget_exhausted"
// when it is hit. Pass zero for DefaultMaxInstructions.
//
// Cancelling ctx abandons the in-flight response, which leaves the request/response stream
// unrecoverable. Run therefore kills the server on cancellation, and every later call on the same
// Client fails immediately.
func (c *Client) Run(ctx context.Context, source string, initial State, maxInstructions uint64) (Result, error) {
	if maxInstructions == 0 {
		maxInstructions = DefaultMaxInstructions
	}
	req := request{
		Code:            source,
		MaxInstructions: maxInstructions,
		// One tick per instruction is the worst a yield-heavy program can do, so this makes the
		// instruction budget the only one that ever bites.
		MaxTicks:  maxInstructions + 1,
		TimeoutMS: serverTimeout(ctx).Milliseconds(),
	}
	req.Registers = make([]string, RegisterCount)
	for i, v := range initial.Registers {
		req.Registers[i] = encodeBits(v)
	}
	for addr, v := range initial.Stack {
		// Compared on bits, not value, so a seeded -0 is sent rather than treated as empty.
		if math.Float64bits(v) != 0 {
			req.Stack = append(req.Stack, stackEntry{Address: addr, Bits: encodeBits(v)})
		}
	}

	line, err := json.Marshal(req)
	if err != nil {
		return Result{}, fmt.Errorf("marshal request: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken != nil {
		return Result{}, fmt.Errorf("oracle stream is unusable: %w", c.broken)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("before request: %w", err)
	}

	type outcome struct {
		raw []byte
		err error
	}
	done := make(chan outcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if _, err := c.stdin.Write(append(line, '\n')); err != nil {
			done <- outcome{err: fmt.Errorf("write request: %w", err)}
			return
		}
		raw, err := c.stdout.ReadBytes('\n')
		if err != nil {
			done <- outcome{err: fmt.Errorf("read response: %w", err)}
			return
		}
		done <- outcome{raw: raw}
	}()

	var raw []byte
	select {
	case got := <-done:
		if got.err != nil {
			c.broken = fmt.Errorf("%w%s", got.err, c.stderr.suffix())
			return Result{}, c.broken
		}
		raw = got.raw
	case <-ctx.Done():
		c.broken = errors.Join(fmt.Errorf("request abandoned: %w", ctx.Err()), c.kill(finished))
		return Result{}, c.broken
	}

	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.broken = fmt.Errorf("unmarshal response: %w", err)
		return Result{}, c.broken
	}
	if !resp.OK {
		return Result{}, fmt.Errorf("oracle rejected program: %s", resp.ErrorMsg)
	}
	if len(resp.Registers) != RegisterCount {
		c.broken = fmt.Errorf("oracle returned %d registers, want %d", len(resp.Registers), RegisterCount)
		return Result{}, c.broken
	}

	result := Result{
		InstructionPointer: resp.IP,
		Status:             resp.State,
		ErrorType:          resp.ErrorType,
		ErrorLine:          resp.ErrorLine,
		ErrorMsg:           resp.ErrorMsg,
		CompileErrors:      resp.CompileErrors,
		Instructions:       resp.Instructions,
		Ticks:              resp.Ticks,
	}
	for i, bits := range resp.Registers {
		if result.Final.Registers[i], err = decodeBits(bits); err != nil {
			return Result{}, fmt.Errorf("register %s: %w", RegisterName(i), err)
		}
	}
	for _, slot := range resp.Stack {
		if slot.Address < 0 || slot.Address >= ic10.NumMemorySlots {
			return Result{}, fmt.Errorf("oracle returned slot %d, outside 0..%d", slot.Address, ic10.NumMemorySlots-1)
		}
		if result.Final.Stack[slot.Address], err = decodeBits(slot.Bits); err != nil {
			return Result{}, fmt.Errorf("stack[%d]: %w", slot.Address, err)
		}
	}
	return result, nil
}

// serverTimeout is the deadline the server should apply to one program: a little inside the
// caller's own deadline, so a hung program comes back as a result rather than as a cancellation
// that poisons the client.
func serverTimeout(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultServerTimeout
	}
	remaining := time.Until(deadline) - serverTimeoutMargin
	if remaining < time.Millisecond {
		return time.Millisecond
	}
	return remaining
}

func encodeBits(v float64) string {
	return strconv.FormatUint(math.Float64bits(v), 10)
}

func decodeBits(s string) (float64, error) {
	bits, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("decode bit pattern %q: %w", s, err)
	}
	return math.Float64frombits(bits), nil
}

// RegisterName renders a register index the way IC10 source does.
func RegisterName(i int) string {
	switch i {
	case 16:
		return "sp"
	case 17:
		return "ra"
	default:
		return fmt.Sprintf("r%d", i)
	}
}

// tailBuffer keeps the last limit bytes written to it, so a server that dies noisily can explain
// itself in the error the client returns.
type tailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailBuffer) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) == 0 {
		return ""
	}
	return "; stderr: " + string(t.buf)
}
