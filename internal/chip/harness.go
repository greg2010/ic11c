package chip

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/greg2010/ic11c/internal/ic10"
)

// chipBinary is the name task chip:build writes into the output directory.
const chipBinary = "chip.exe"

// mountPoint is where the binary directory appears inside the container. It is
// read-only: the harness is a server, not a build step.
const mountPoint = "/out"

// fixturesFlag is the harness argument that lets a process build a device
// answering any property. It is read from argv before the first command and no
// command changes it; see the package doc.
const fixturesFlag = "--fixtures"

// defaultCommandTimeout bounds one exchange. It is generous because the first
// command pays Mono's startup, and it exists so that a wedged process surfaces
// as an error naming the command it wedged on instead of hanging the caller.
const defaultCommandTimeout = 60 * time.Second

// stderrTail is how much of the container's stderr is kept for error messages.
// A Mono stack trace is worth reading and an unbounded buffer is not.
const stderrTail = 8 << 10

// Options configure a chip process.
type Options struct {
	// Image is the Mono image to run, pinned by digest. It must be the digest
	// tools/chipgen's task targets build against: the image decides what the chip's
	// answers are, and a finding under a different one is not reproducible.
	Image string
	// BinDir is the host directory holding chip.exe, mounted read-only.
	BinDir string
	// CommandTimeout bounds one exchange; zero means one minute.
	CommandTimeout time.Duration
	// Log receives one line per lifecycle event. Nil discards them.
	Log func(format string, args ...any)
}

// Harness is one long-lived chip process, faithful in every device, since
// starting one per program costs roughly half a second of Mono startup — far
// more than running a program takes. It is not safe for concurrent use: the
// protocol is a single request-response stream.
type Harness struct {
	cmd       *exec.Cmd
	container string
	timeout   time.Duration
	log       func(format string, args ...any)
	// fixtures records which kind of process this is. It is set once, from the
	// argv Start built, and nothing else writes it: a harness cannot be talked
	// into becoming the other kind, only replaced by one.
	fixtures bool

	stdin *bufio.Writer
	lines chan string
	// readErr is why the reader stopped, set once before lines closes.
	readErr error
	stderr  *tailBuffer

	// broken is the first exchange that failed; refusing every later one keeps
	// a caller from reading a reply left over from it. Every failure mode
	// leaves replies in flight — a refused batch command, a late deadline
	// arrival, an unparsed block — that would otherwise answer the next
	// command in its place.
	broken error

	closeOnce sync.Once
	closeErr  error
}

// Start launches a faithful chip process and probes that it answers. ctx
// bounds the process's whole life: cancelling it kills the container. Every
// startup failure is wrapped in [ErrUnavailable]. The caller must Close the
// returned harness; Close force-removes the container even if the process
// already exited.
func Start(ctx context.Context, opts Options) (*Harness, error) {
	return start(ctx, opts, false)
}

// StartFixtures launches a permissive chip process: one that can build
// devices answering any property and recording what the program wrote. It
// returns a distinct type carrying the verbs that reach that behaviour.
func StartFixtures(ctx context.Context, opts Options) (*FixtureHarness, error) {
	h, err := start(ctx, opts, true)
	if err != nil {
		return nil, err
	}
	return &FixtureHarness{chipProcess: h}, nil
}

func start(ctx context.Context, opts Options, fixtures bool) (*Harness, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("%w: no image given", ErrUnavailable)
	}
	if !strings.Contains(opts.Image, "@sha256:") {
		return nil, fmt.Errorf("%w: image %q is not pinned by digest", ErrUnavailable, opts.Image)
	}
	binDir, err := filepath.Abs(opts.BinDir)
	if err != nil {
		return nil, fmt.Errorf("%w: binary directory %q: %w", ErrUnavailable, opts.BinDir, err)
	}
	if _, err := os.Stat(filepath.Join(binDir, chipBinary)); err != nil {
		return nil, fmt.Errorf("%w: no %s under %s: %w", ErrUnavailable, chipBinary, binDir, err)
	}

	name, err := containerName()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	h := &Harness{
		container: name,
		timeout:   opts.CommandTimeout,
		log:       opts.Log,
		fixtures:  fixtures,
		lines:     make(chan string, 64),
		stderr:    &tailBuffer{limit: stderrTail},
	}
	if h.timeout == 0 {
		h.timeout = defaultCommandTimeout
	}
	if h.log == nil {
		h.log = func(string, ...any) {}
	}

	// --pull=never so that a missing image is an immediate, named failure rather
	// than a pull this run has no business making. --network none because the
	// chip reaches nothing, and the memory bound is on the container rather than
	// on the caller because the container is the daemon's child and not this
	// process's.
	argv := []string{"run",
		"--rm", "-i", "--name", name, "--pull=never", "--network", "none",
		"--memory=2g", "--memory-swap=2g",
		"-v", binDir + ":" + mountPoint + ":ro",
		opts.Image, "mono", mountPoint + "/" + chipBinary}
	if fixtures {
		argv = append(argv, fixturesFlag)
	}
	h.cmd = exec.CommandContext(ctx, "docker", argv...)
	h.cmd.Stderr = h.stderr

	stdin, err := h.cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdin pipe: %w", ErrUnavailable, err)
	}
	stdout, err := h.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdout pipe: %w", ErrUnavailable, err)
	}
	if err := h.cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start docker: %w", ErrUnavailable, err)
	}
	h.stdin = bufio.NewWriter(stdin)
	go h.read(stdout)

	if err := h.probe(ctx); err != nil {
		// The probe failed, so nothing this harness could say is worth having.
		// Close still runs, because the container is running either way.
		if closeErr := h.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w (and closing it: %w)", err, closeErr)
		}
		return nil, err
	}
	return h, nil
}

func containerName() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("name the container: %w", err)
	}
	return "ic11c-chip-" + hex.EncodeToString(suffix[:]), nil
}

// read pumps the harness's stdout into a channel so that every wait for a reply
// can carry a deadline. An os pipe has none of its own, and without one a
// harness that stops answering hangs the caller with no attribution.
func (h *Harness) read(stdout io.Reader) {
	defer close(h.lines)
	scanner := bufio.NewScanner(stdout)
	// A state block's regs line carries 18 doubles and a stack line one, so the
	// default 64KiB is ample; the limit is raised only so that a protocol change
	// producing longer lines fails as a parse error rather than as a truncation.
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		h.lines <- scanner.Text()
	}
	h.readErr = scanner.Err()
}

// Close shuts the harness down and removes its container. It sends quit,
// waits for the process, and force-removes the container regardless; a
// harness that will not quit is killed and reported, since a leftover
// container on a machine that runs this in a loop is a defect. A value no
// constructor built is refused with [ErrUnavailable] rather than reported as
// a clean shutdown.
func (h *Harness) Close() error {
	if h == nil || h.cmd == nil {
		return errNoProcess()
	}
	h.closeOnce.Do(func() { h.closeErr = h.shutdown() })
	return h.closeErr
}

func (h *Harness) shutdown() error {
	var problems []error
	if err := h.send(cmdQuit); err != nil && !gone(err) {
		problems = append(problems, fmt.Errorf("send quit: %w", err))
	}

	done := make(chan error, 1)
	go func() { done <- h.cmd.Wait() }()
	select {
	case err := <-done:
		problems = append(problems, waited(err, "wait for docker")...)
	case <-time.After(h.timeout):
		problems = append(problems, fmt.Errorf("harness did not exit within %s", h.timeout))
		if err := h.cmd.Process.Kill(); err != nil {
			problems = append(problems, fmt.Errorf("kill the docker client: %w", err))
		}
		problems = append(problems, waited(<-done, "wait for the docker client this kill signalled")...)
	}

	if err := h.removeContainer(); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// waited reads what waiting for the docker client produced, dropping the one
// outcome that is not a shutdown problem: a non-zero exit is expected, either
// from the container's own command or from the kill above. Anything else means
// the wait itself failed, which is what the caller needs told.
func waited(err error, what string) []error {
	var exit *exec.ExitError
	if err == nil || errors.As(err, &exit) {
		return nil
	}
	return []error{fmt.Errorf("%s: %w", what, err)}
}

// gone reports whether a write failed because the process is already gone,
// which is not a shutdown problem: quit is a courtesy to a process that is
// already exiting. All three spellings are matched because which one surfaces
// depends on where the pipe was torn down — often before cleanup runs, since a
// harness's context is often a test's and is cancelled first.
func gone(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EPIPE)
}

// removalPoll is how often removeContainer asks again while --rm's own teardown
// is still running.
const removalPoll = 50 * time.Millisecond

// removeContainer is the backstop behind --rm, closing the leak of a
// container the daemon still holds after the client has gone. It returns only
// once the container is actually gone: --rm tears the same container down
// concurrently, so the first attempt often reports "already in progress"
// rather than done.
func (h *Harness) removeContainer() error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), h.timeout)
	defer cancel()

	for attempt := 1; ; attempt++ {
		cmd := exec.CommandContext(ctx, "docker", "rm", "--force", "--volumes", h.container)
		output, err := cmd.CombinedOutput()
		switch {
		case err == nil:
			h.log("removed container %s", h.container)
			return nil
		case strings.Contains(string(output), "No such container"):
			return nil
		case !strings.Contains(string(output), "already in progress"):
			return fmt.Errorf("remove container %s: %w: %s", h.container, err, strings.TrimSpace(string(output)))
		}
		select {
		case <-time.After(removalPoll):
		case <-ctx.Done():
			return fmt.Errorf("container %s was still being removed after %d attempts over %s: %w",
				h.container, attempt, h.timeout, ctx.Err())
		}
	}
}

func (h *Harness) send(lines ...string) error {
	for _, line := range lines {
		if _, err := h.stdin.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("write %q: %w", verb(line), err)
		}
	}
	if err := h.stdin.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}

// verb is the first word of a command, for an error message that names what
// went wrong without quoting a base64 program at the reader.
func verb(line string) string {
	if before, _, ok := strings.Cut(line, " "); ok {
		return before
	}
	return line
}

func (h *Harness) readLine(ctx context.Context) (string, error) {
	timer := time.NewTimer(h.timeout)
	defer timer.Stop()
	select {
	case line, ok := <-h.lines:
		if !ok {
			return "", fmt.Errorf("%w: harness closed its output: %w%s",
				ErrUnavailable, errors.Join(h.readErr, io.EOF), h.stderr.report())
		}
		return line, nil
	case <-timer.C:
		return "", fmt.Errorf("%w: no reply within %s%s", ErrUnavailable, h.timeout, h.stderr.report())
	case <-ctx.Done():
		return "", fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
}

// errNoProcess is what every verb answers on a value no constructor built. A
// zero [Harness] or [FixtureHarness] reaches every verb — the latter by
// promotion through a nil embedded pointer — with no process behind it; this
// turns that into a named error rather than a nil dereference.
func errNoProcess() error {
	return fmt.Errorf("%w: this value carries no chip process, and only Start and StartFixtures build one",
		ErrUnavailable)
}

// begin refuses an exchange on a harness no constructor built or a previous
// exchange broke. See [Harness].
func (h *Harness) begin() error {
	if h == nil || h.stdin == nil {
		return errNoProcess()
	}
	if h.broken != nil {
		return fmt.Errorf("%w: an earlier exchange left the stream out of step: %w",
			ErrUnavailable, h.broken)
	}
	return nil
}

// fail records why an exchange stopped and returns it unchanged.
func (h *Harness) fail(err error) error {
	if h.broken == nil {
		h.broken = err
	}
	return err
}

// do sends commands that each answer with one line and checks every answer.
func (h *Harness) do(ctx context.Context, commands ...string) error {
	if err := h.begin(); err != nil {
		return err
	}
	if err := h.send(commands...); err != nil {
		return h.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	return h.expectOK(ctx, commands)
}

// expectOK reads one line per command and checks each; an "err" reply means
// this package sent something the harness could not read, a bug here rather
// than a chip verdict. Commands must already be sent. It is split from
// [Harness.do] for exchanges that read more than one line per command — a
// batch ending in a block or value command is checked up to there with this,
// and the rest read past it by the caller.
func (h *Harness) expectOK(ctx context.Context, commands []string) error {
	for _, command := range commands {
		line, err := h.readLine(ctx)
		if err != nil {
			return h.fail(fmt.Errorf("%s: %w", verb(command), err))
		}
		if !strings.HasPrefix(line, okPrefix) {
			return h.fail(fmt.Errorf("%w: %s: harness answered %q", ErrUnavailable, verb(command), line))
		}
	}
	return nil
}

// query sends one command whose answer carries a double, and returns it. The
// value is required rather than defaulted: a bare "ok" reply is one this
// package cannot attribute, and reading it as zero would put a number nobody
// measured where a measurement was asked for.
func (h *Harness) query(ctx context.Context, command string) (float64, error) {
	if err := h.begin(); err != nil {
		return 0, err
	}
	if err := h.send(command); err != nil {
		return 0, h.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	line, err := h.readLine(ctx)
	if err != nil {
		return 0, h.fail(fmt.Errorf("%s: %w", verb(command), err))
	}
	answer, ok := strings.CutPrefix(line, okPrefix+" ")
	if !ok {
		return 0, h.fail(fmt.Errorf("%w: %s: harness answered %q, want %q and a value",
			ErrUnavailable, verb(command), line, okPrefix))
	}
	value, err := parseBits(answer)
	if err != nil {
		return 0, h.fail(fmt.Errorf("%w: %s: %w", ErrUnavailable, verb(command), err))
	}
	return value, nil
}

// readBlock consumes a block already asked for, up to but not including its
// terminator. It does not treat a leading "err" as a failed command, since
// "err" is also the state block's own key for the runtime error — the two are
// told apart by position, not spelling. A reply that isn't a block at all is
// caught by the line cap and the parse.
func (h *Harness) readBlock(ctx context.Context, kind string, maxLines int) ([]string, error) {
	if maxLines < 0 {
		return nil, h.fail(fmt.Errorf("%w: %s: a block of at most %d lines is not a block",
			ErrUnavailable, kind, maxLines))
	}
	var body []string
	for len(body) <= maxLines {
		line, err := h.readLine(ctx)
		if err != nil {
			return nil, h.fail(fmt.Errorf("%s: %w", kind, err))
		}
		if line == blockEnd {
			return body, nil
		}
		body = append(body, line)
	}
	return nil, h.fail(fmt.Errorf("%w: %s: no %q after %d lines, first was %q",
		ErrUnavailable, kind, blockEnd, len(body), body[0]))
}

// maxStateLines is the longest a state block can legitimately be: one line per
// key the block must carry, and one per memory slot on top. A reply that runs
// past it is not a block, and stopping there is what keeps a harness answering
// something else from being read until the deadline.
func (h *Harness) maxStateLines() int {
	return len(requiredStateKeys(h.fixtures)) + ic10.NumMemorySlots
}

func (h *Harness) readSnapshot(ctx context.Context) (Snapshot, error) {
	body, err := h.readBlock(ctx, cmdState, h.maxStateLines())
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := parseSnapshot(body, h.fixtures)
	if err != nil {
		return Snapshot{}, h.fail(fmt.Errorf("%w: state: %w", ErrUnavailable, err))
	}
	return snapshot, nil
}

// State reads the chip's whole state.
func (h *Harness) State(ctx context.Context) (Snapshot, error) {
	if err := h.begin(); err != nil {
		return Snapshot{}, err
	}
	if err := h.send(cmdState); err != nil {
		return Snapshot{}, h.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	return h.readSnapshot(ctx)
}

// probe establishes that the process is answering and that a double survives
// the round trip, before anything depends on either.
func (h *Harness) probe(ctx context.Context) error {
	if err := h.probeValues(ctx); err != nil {
		return err
	}

	// The chip computes a negative zero and divides into it, so the sign is read
	// twice over: once off the zero itself and once off the infinity its
	// reciprocal is, which is a value with no zero in it at all.
	const signSource = "mul r0 0 -1\ndiv r1 1 r0"
	if err := h.do(ctx, cmdReset, cmdSource+" "+encodeText(signSource), cmdRun+" 128"); err != nil {
		return fmt.Errorf("sign probe: %w", err)
	}
	snapshot, err := h.State(ctx)
	if err != nil {
		return fmt.Errorf("sign probe: %w", err)
	}
	if snapshot.Fault.Type != ExcNone || snapshot.CompileError.Type != ExcNone {
		return fmt.Errorf("%w: sign probe faulted: %s, compile %s",
			ErrUnavailable, snapshot.Fault, snapshot.CompileError)
	}
	if bits := math.Float64bits(snapshot.Registers[0]); bits != math.Float64bits(math.Copysign(0, -1)) {
		return fmt.Errorf("%w: the chip computed 0*-1 as %016x, so the state block is not "+
			"reporting the sign of a zero", ErrUnavailable, bits)
	}
	if !math.IsInf(snapshot.Registers[1], -1) {
		return fmt.Errorf("%w: 1/(0*-1) came back as %016x rather than negative infinity",
			ErrUnavailable, math.Float64bits(snapshot.Registers[1]))
	}
	// The probe leaves its own program and registers on the chip, so Start hands
	// back a fresh one rather than a caller's first program running on top of
	// whatever the probe computed.
	if err := h.Reset(ctx); err != nil {
		return fmt.Errorf("clear the probe: %w", err)
	}

	h.log("chip ready: fixtures=%t", h.fixtures)
	return nil
}

// probeValues seeds values a decimal protocol could not carry and reads them
// back through the state block rather than the verb that wrote them, so a
// seeding command that answers ok while landing nowhere — a dropped value, a
// folded sign — is caught here instead of surfacing later as arithmetic that
// disagrees.
func (h *Harness) probeValues(ctx context.Context) error {
	probes := []float64{
		math.Copysign(0, -1),
		math.Float64frombits(0x7ff8000000dead01),
		math.Inf(-1),
		math.MaxFloat64,
	}
	commands := []string{cmdReset}
	for i, value := range probes {
		commands = append(commands,
			cmdReg+" "+strconv.Itoa(i)+" "+formatBits(value),
			cmdStack+" "+strconv.Itoa(i)+" "+formatBits(value))
	}
	if err := h.do(ctx, commands...); err != nil {
		return fmt.Errorf("round trip probe: %w", err)
	}
	snapshot, err := h.State(ctx)
	if err != nil {
		return fmt.Errorf("round trip probe: %w", err)
	}
	for i, want := range probes {
		for _, read := range []struct {
			where string
			got   float64
		}{
			{"r" + strconv.Itoa(i), snapshot.Registers[i]},
			{"stack[" + strconv.Itoa(i) + "]", snapshot.Stack[i]},
		} {
			if math.Float64bits(read.got) != math.Float64bits(want) {
				return fmt.Errorf("%w: %s was seeded with %016x and read back as %016x, so the "+
					"protocol is not carrying a double whole", ErrUnavailable,
					read.where, math.Float64bits(want), math.Float64bits(read.got))
			}
		}
	}
	return nil
}

// tailBuffer keeps the last bytes written to it, which is what a Mono stack
// trace needs to be worth reporting without an unbounded buffer behind it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = b.data[len(b.data)-b.limit:]
	}
	return len(p), nil
}

// report renders the tail for an error message, or nothing when the container
// said nothing.
func (b *tailBuffer) report() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := strings.TrimSpace(string(b.data))
	if text == "" {
		return ""
	}
	return "\ncontainer stderr:\n" + text
}
