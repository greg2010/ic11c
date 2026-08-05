package chip

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// InstructionsPerTick is the budget CircuitHousing.Execute spends per tick in
// game; a caller reproducing the game's own segmentation steps with this. It is
// a copy of CircuitHousing.RUN_COUNT, which tools/chipgen lifts and the slice
// digest fingerprints; [Harness.Limits] reads the lifted value back, holding
// this copy to the game's declaration by a run rather than a second reading.
const InstructionsPerTick = 128

// TicksPerRun bounds [Harness.Run]. It is far more ticks than a terminating
// program takes, and it is here so that a program that never ends stops the run
// instead of wedging it.
const TicksPerRun = 1024

// StopReason is why a run or a segment ended.
type StopReason string

const (
	// StopEnded means the program counter left the program.
	StopEnded StopReason = "ended"
	// StopFaulted means the chip recorded a run time error and parked on the
	// faulting line.
	StopFaulted StopReason = "faulted"
	// StopCompileError means the program did not compile. The chip runs whatever
	// prefix did compile rather than refusing the run.
	StopCompileError StopReason = "compile_error"
	// StopSuspended means a yield or a sleep ended the segment with the program
	// still inside itself. Only [Harness.Step] reports it; [Harness.Run] carries
	// on to the next tick, which is what the game does.
	StopSuspended StopReason = "suspended"
	// StopBudget means the segment ran out of the instructions it was given.
	// Only [Harness.Step] reports it, for the same reason.
	StopBudget StopReason = "budget"
	// StopTickBudget means [Harness.Run] was cut off after [TicksPerRun] with
	// the program still going.
	StopTickBudget StopReason = "tick_budget"
)

// State is a machine state to seed.
type State struct {
	Registers [ic10.NumRegisters]float64
	Stack     [ic10.NumMemorySlots]float64
}

// Request is one program and the state it starts from.
type Request struct {
	// Source is IC10 assembly. Nothing about it is normalised, a trailing
	// newline included: the chip compiles one to an extra empty line and so
	// must anything driving it.
	Source string
	// Initial is the machine state to seed after the program is loaded.
	Initial State
}

// Segment is what one call to [Harness.Step] did.
type Segment struct {
	Snapshot
	Stop StopReason
}

// Observation is what one whole run did.
type Observation struct {
	Snapshot
	// Ticks is how many times Execute was called before the run stopped, and is
	// zero for a program that did not compile.
	Ticks int
	Stop  StopReason
}

// Reset builds a fresh housing and chip, discarding the loaded program and
// everything the last one left behind. The clock and the random seed survive
// it, because a caller that armed them armed them for the run.
func (h *Harness) Reset(ctx context.Context) error {
	return h.do(ctx, cmdReset)
}

// Load compiles source onto the chip. A program that does not compile is not
// an error here — the refusal is [Snapshot.CompileError], a chip verdict.
// Loading zeroes the stack pointer, so a state seeded before it loses that
// register; [Harness.Seed] must run after this.
func (h *Harness) Load(ctx context.Context, source string) error {
	return h.do(ctx, cmdSource+" "+encodeText(source))
}

// setupCommands puts a program and the state it starts from onto a fresh
// chip; the seed goes after the load, for the reason [Harness.Load] gives.
func setupCommands(req Request) []string {
	return append([]string{cmdReset, cmdSource + " " + encodeText(req.Source)},
		seedCommands(req.Initial)...)
}

// Seed writes a machine state onto the chip. It must run after [Harness.Load];
// see there.
func (h *Harness) Seed(ctx context.Context, initial State) error {
	return h.do(ctx, seedCommands(initial)...)
}

// seedCommands writes every field the chip does not already hold. A field is
// skipped on its bit pattern, not equality, since reset leaves +0.0 and -0.0
// compares equal to it — skipping on equality would silently seed a -0.0 the
// caller never asked for.
func seedCommands(initial State) []string {
	var commands []string
	for i, value := range initial.Registers {
		if math.Float64bits(value) == 0 {
			continue
		}
		commands = append(commands, cmdReg+" "+strconv.Itoa(i)+" "+formatBits(value))
	}
	for address, value := range initial.Stack {
		if math.Float64bits(value) == 0 {
			continue
		}
		commands = append(commands, cmdStack+" "+strconv.Itoa(address)+" "+formatBits(value))
	}
	return commands
}

// SetClock pins the sleep clock at value and says how far one reading advances
// it. A step of zero is indistinguishable across two readings, so a sleep under
// it never expires; _SLEEP_Operation subtracts the difference between readings,
// so a step of s removes s from the remaining duration each time. The clock is
// a float where everything else here is a double, and the harness rounds to
// the nearest float.
func (h *Harness) SetClock(ctx context.Context, value, step float64) error {
	return h.do(ctx, cmdClock+" "+formatBits(value)+" "+formatBits(step))
}

// SetRandomSeed arms the sequence rand draws from and restarts it.
//
// No seed reproduces the game's own unseeded generator. What this buys is that
// the sequence is a function of the seed, so two runs of one program draw the
// same numbers.
func (h *Harness) SetRandomSeed(ctx context.Context, seed int) error {
	return h.do(ctx, cmdSeed+" "+strconv.Itoa(seed))
}

// SetRegister writes one register, whatever the chip already holds there,
// unlike [Harness.Seed] which skips fields the chip already holds. That
// matters to a caller writing over a running program: a zero has to land, or
// the register keeps its old value for the next instruction.
func (h *Harness) SetRegister(ctx context.Context, register ic10.Register, value float64) error {
	if int(register) >= ic10.NumRegisters {
		return fmt.Errorf("%w: r%d is outside the chip's %d registers",
			ErrUnavailable, register, ic10.NumRegisters)
	}
	return h.do(ctx, cmdReg+" "+strconv.FormatUint(uint64(register), 10)+" "+formatBits(value))
}

// SetRegisters writes values into the general registers from r0 upward. Each is
// written unconditionally; see [Harness.SetRegister].
func (h *Harness) SetRegisters(ctx context.Context, values ...float64) error {
	if len(values) > ic10.NumRegisters {
		return fmt.Errorf("%w: %d values is more than the %d registers the chip has",
			ErrUnavailable, len(values), ic10.NumRegisters)
	}
	commands := make([]string, len(values))
	for i, value := range values {
		commands[i] = cmdReg + " " + strconv.Itoa(i) + " " + formatBits(value)
	}
	return h.do(ctx, commands...)
}

// SetAddress moves the program counter to line, which must be inside the
// loaded program; the harness refuses an address outside it rather than
// parking the chip somewhere a later run would read as ended. It exists for a
// caller driving the chip one instruction at a time as a dispatcher — a caller
// running a program to its own stop never needs it.
func (h *Harness) SetAddress(ctx context.Context, line int) error {
	return h.do(ctx, cmdAddress+" "+strconv.Itoa(line))
}

// Property reads one logic property off a device, through GetLogicValue — the
// same path the l instruction takes, so a value this answers is one a program
// could have read. It's what a caller checking what a program left behind
// needs, since a trace carries only what changed. target is a pin number, or
// [Housing] for the chip's own housing.
func (h *Harness) Property(ctx context.Context, target string, property ic10.LogicType) (float64, error) {
	return h.query(ctx, cmdGet+" "+target+" "+strconv.FormatUint(uint64(property), 10))
}

// SlotProperty reads one slot property off a device. See [Harness.Property].
func (h *Harness) SlotProperty(ctx context.Context, target string, slot int, property ic10.LogicSlotType) (float64, error) {
	return h.query(ctx, cmdGetSlot+" "+target+" "+strconv.Itoa(slot)+" "+
		strconv.FormatUint(uint64(property), 10))
}

// Housing names the chip's own housing where a verb takes a device, which is
// what db resolves to. Every other target is a pin number.
const Housing = "db"

// Pin names the device on a pin where a verb takes one.
func Pin(pin int) string { return strconv.Itoa(pin) }

// SetDataNetwork runs a data cable to the housing, or takes it away. A reset
// leaves the housing on none: every batch form then raises
// [ExcDeviceListNull], since the game answers a null device list for a null
// network. A cable also gates which pins resolve — with one, the game filters
// pin lookups against the cable's own device list, so a device must also be
// connected via [Harness.ConnectDevice] to be reachable; with none, every pin
// resolves. Not undone by [Harness.Reset], which builds a fresh housing — ask
// again after every reset, including the one [Harness.Run] does.
func (h *Harness) SetDataNetwork(ctx context.Context, connected bool) error {
	return h.do(ctx, cmdDev+" "+Housing+" network "+boolArg(connected))
}

// ConnectDevice puts a device on the housing's data cable, which is what makes
// it one of the devices a batch form folds over and a pin lookup can reach.
//
// The housing must already be on a cable; see [Harness.SetDataNetwork].
func (h *Harness) ConnectDevice(ctx context.Context, target string) error {
	return h.do(ctx, cmdDev+" "+target+" batch")
}

func boolArg(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// SetFlag decides which properties a target publishes, the game's own gate on
// whether a read or write of one is permitted at all. In the game these come
// off the prefab; here they're set directly, so a run can drive every
// combination. Flag names are the device shim's fields: IsStructureCompleted,
// HasAnySlots, HasReadableAtmosphere, HasReadableReagentMixture, and the eight
// Has*State flags behind the interactable properties. target is [Housing] or a
// pin holding a device.
func (h *Harness) SetFlag(ctx context.Context, target, flag string, on bool) error {
	return h.do(ctx, cmdDev+" "+target+" flag "+flag+" "+boolArg(on))
}

// SetState puts a target into a state a program is meant to read back,
// writing the store the game's getter reads rather than going through a logic
// write. That lets a run reach a state no logic write could — a mode a prefab
// doesn't name, an error nobody raised — though a property the game computes
// rather than stores is refused, not faked. target is [Housing] or a pin
// holding a device.
func (h *Harness) SetState(ctx context.Context, target string, property ic10.LogicType, value float64) error {
	return h.do(ctx, cmdDev+" "+target+" set "+strconv.FormatUint(uint64(property), 10)+" "+formatBits(value))
}

// SetSynced decides whether the interactable behind one device state is one
// the game synchronises. The game reads such a state through an interactable
// that answers zero unless the flag is set; every harness device is built
// with it set, and clearing it asks what those arms answer for a state the
// game never syncs. property must be one of the eight states an interactable
// stands behind: On, Open, Lock, Mode, Error, Activate, Color or Power.
// target is [Housing] or a pin holding a device.
func (h *Harness) SetSynced(ctx context.Context, target string, property ic10.LogicType, synced bool) error {
	return h.do(ctx, cmdDev+" "+target+" sync "+strconv.FormatUint(uint64(property), 10)+" "+boolArg(synced))
}

// Limits are the machine sizes the compiled slice holds, mirrored by constants
// in this package and internal/ic10. tools/chipgen lifts both from the game's
// C# and the slice digest fingerprints them, so a game update that moves one
// stops the slice — though not the Go constant mirroring it, which this run
// exists to catch instead.
type Limits struct {
	// InstructionsPerTick is CircuitHousing.RUN_COUNT.
	InstructionsPerTick int
	// DevicePins is the length of CircuitHousing.Devices.
	DevicePins int
}

// Limits reads the machine sizes off the chip. See [Limits].
func (h *Harness) Limits(ctx context.Context) (Limits, error) {
	if err := h.begin(); err != nil {
		return Limits{}, err
	}
	if err := h.send(cmdLimits); err != nil {
		return Limits{}, h.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	line, err := h.readLine(ctx)
	if err != nil {
		return Limits{}, h.fail(fmt.Errorf("%s: %w", cmdLimits, err))
	}
	answer, ok := strings.CutPrefix(line, okPrefix+" ")
	if !ok {
		return Limits{}, h.fail(fmt.Errorf("%w: %s: harness answered %q, want %q and two counts",
			ErrUnavailable, cmdLimits, line, okPrefix))
	}
	fields := strings.Fields(answer)
	if len(fields) != 2 {
		return Limits{}, h.fail(fmt.Errorf("%w: %s: harness answered %q, want a tick budget and a pin count",
			ErrUnavailable, cmdLimits, line))
	}
	var limits Limits
	for _, field := range []struct {
		what string
		dest *int
		text string
	}{
		{"tick budget", &limits.InstructionsPerTick, fields[0]},
		{"pin count", &limits.DevicePins, fields[1]},
	} {
		value, err := strconv.Atoi(field.text)
		if err != nil {
			return Limits{}, h.fail(fmt.Errorf("%w: %s: %s %q: %w",
				ErrUnavailable, cmdLimits, field.what, field.text, err))
		}
		*field.dest = value
	}
	return limits, nil
}

// Step retires up to budget instructions and reports what the chip did. The
// run and the state read go out together, so a segment costs one round trip;
// there's no cheaper way to ask whether the chip stopped, since the protocol's
// only query is the whole state block.
func (h *Harness) Step(ctx context.Context, budget int) (Segment, error) {
	if budget <= 0 {
		return Segment{}, fmt.Errorf("%w: a segment of %d instructions retires nothing", ErrUnavailable, budget)
	}
	if err := h.begin(); err != nil {
		return Segment{}, err
	}
	if err := h.send(cmdRun+" "+strconv.Itoa(budget), cmdState); err != nil {
		return Segment{}, h.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	line, err := h.readLine(ctx)
	if err != nil {
		return Segment{}, h.fail(fmt.Errorf("%s: %w", cmdRun, err))
	}
	var exhausted bool
	switch line {
	case runExhausted:
		exhausted = true
	case runStopped:
	default:
		return Segment{}, h.fail(fmt.Errorf("%w: %s: harness answered %q, want %q or %q",
			ErrUnavailable, cmdRun, line, runStopped, runExhausted))
	}
	snapshot, err := h.readSnapshot(ctx)
	if err != nil {
		return Segment{}, err
	}
	return Segment{Snapshot: snapshot, Stop: stopReason(snapshot, exhausted)}, nil
}

// stopReason reads why a segment ended. The budget is consulted last since
// it's the only ending the chip doesn't otherwise record — every other ending
// leaves the flag clear. It's necessary rather than convenient because a yield
// leaves the same address, error state and line count as an overrun (the sign
// of _YIELD_Operation's negative return is lost), and because
// _SLEEP_Operation returns -index, so a sleep on line 0 returns -0, does not
// suspend, and spins the rest of the budget exactly where a suspended sleep
// would have left it.
func stopReason(snapshot Snapshot, exhausted bool) StopReason {
	switch {
	case snapshot.CompileError.Type != ExcNone:
		return StopCompileError
	case snapshot.Fault.Type != ExcNone:
		return StopFaulted
	case snapshot.Address < 0 || snapshot.Address >= snapshot.LineCount:
		return StopEnded
	case exhausted:
		return StopBudget
	}
	return StopSuspended
}

// Run loads a program, seeds the machine and ticks it to a stop with
// [InstructionsPerTick] per tick and at most [TicksPerRun] ticks, in one
// exchange — the tick loop runs inside the harness process rather than costing
// a round trip per tick. Every error wraps [ErrUnavailable]: a chip verdict,
// fault or compile error included, is an Observation, so an error here always
// means the program did not run.
func (h *Harness) Run(ctx context.Context, req Request) (Observation, error) {
	if err := h.begin(); err != nil {
		return Observation{}, err
	}
	setup := setupCommands(req)
	drive := cmdRunTo + " " + strconv.Itoa(InstructionsPerTick) + " " + strconv.Itoa(TicksPerRun)
	if err := h.send(append(slices.Clone(setup), drive, cmdState)...); err != nil {
		return Observation{}, h.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	if err := h.expectOK(ctx, setup); err != nil {
		return Observation{}, err
	}
	line, err := h.readLine(ctx)
	if err != nil {
		return Observation{}, h.fail(fmt.Errorf("%s: %w", cmdRunTo, err))
	}
	reason, ticks, err := parseRunTo(line)
	if err != nil {
		return Observation{}, h.fail(fmt.Errorf("%w: %s: %w", ErrUnavailable, cmdRunTo, err))
	}
	snapshot, err := h.readSnapshot(ctx)
	if err != nil {
		return Observation{}, err
	}
	got, err := observation(reason, ticks, snapshot)
	if err != nil {
		return Observation{}, h.fail(err)
	}
	return got, nil
}

// observation reads a whole run's ending out of the harness's reply and the
// state the run left, checking the reply's claimed ending against the one the
// state block implies rather than trusting it — the two are derived from
// opposite sides of the protocol, and only a yield and a spent budget look
// alike in the block, which is why the reply carries an ending at all. A run
// whose last tick left the program still inside itself is a run the tick limit
// stopped, not that tick.
func observation(reason StopReason, ticks int, snapshot Snapshot) (Observation, error) {
	if derived := stopReason(snapshot, reason == StopBudget); derived != reason {
		return Observation{}, fmt.Errorf("%w: the harness ended a run %q and the state it left reads as %q",
			ErrUnavailable, reason, derived)
	}
	if ticks < 1 || ticks > TicksPerRun {
		return Observation{}, fmt.Errorf("%w: the harness ran %d of at most %d ticks",
			ErrUnavailable, ticks, TicksPerRun)
	}
	if reason == StopSuspended || reason == StopBudget {
		if ticks != TicksPerRun {
			return Observation{}, fmt.Errorf("%w: the harness stopped a run %q after %d of %d ticks, "+
				"and a program still inside itself has ticks left to spend",
				ErrUnavailable, reason, ticks, TicksPerRun)
		}
		return Observation{Snapshot: snapshot, Ticks: ticks, Stop: StopTickBudget}, nil
	}
	if reason == StopCompileError {
		// Ticks stays zero; see [Observation.Ticks].
		return Observation{Snapshot: snapshot, Stop: StopCompileError}, nil
	}
	return Observation{Snapshot: snapshot, Ticks: ticks, Stop: reason}, nil
}

// FillStack writes value into every slot of the chip's array, in one exchange,
// including slots that already read zero — a freshly zeroed chip would pass a
// clear-check regardless of whether the program under test cleared anything.
// It exists to avoid paying a round trip per slot.
func (h *Harness) FillStack(ctx context.Context, value float64) error {
	commands := make([]string, ic10.NumMemorySlots)
	for address := range ic10.NumMemorySlots {
		commands[address] = cmdStack + " " + strconv.Itoa(address) + " " + formatBits(value)
	}
	return h.do(ctx, commands...)
}
