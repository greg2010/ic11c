package chip

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// NoSlot marks a write to a device property rather than to one of its slots.
const NoSlot = -1

// Write is one property write a fixture device recorded. Only a write that
// changed something is recorded: the chip itself skips a store that would not
// change the current value, so writing the same value twice produces one
// recorded write or none.
type Write struct {
	// Pin is the housing pin the device sits on, counting from d0.
	Pin int
	// Slot is the slot index, or [NoSlot] for a device property.
	Slot int
	// Property is a LogicType or LogicSlotType ordinal. It is an ordinal rather
	// than a name because the operand forms take a number where they take an
	// enum member, so a program can write a property the enum does not name.
	Property int
	Value    float64
}

// FixtureHarness is a permissive chip process: its devices answer any seeded
// property and record every write, for tracing what a compiled program
// writes. It must never drive a run that checks the faults a real device
// raises, since a permissive device raises none. [StartFixtures] is the only
// constructor; a zero value is unusable and every verb on it returns
// [ErrUnavailable] instead of dereferencing an unstarted process.
type FixtureHarness struct {
	*chipProcess
}

// chipProcess is [Harness] under a name no caller outside this package can
// write. Embedding it gives a permissive harness every verb a faithful one has,
// while leaving nothing that unwraps one back into the type a comparison takes
// or wraps a faithful process into a permissive-looking one.
type chipProcess = Harness

// Seeding collects world changes so they can be sent in one exchange rather
// than one round trip per property. The zero value is ready to use; nothing is
// sent until [FixtureHarness.SeedWorld] takes it, and a Seeding is not safe to
// build from two goroutines.
type Seeding struct {
	commands []string
	err      error
}

// AddDevice puts a recording device on a pin, on the data network the batch
// instructions aggregate over, and under the reference id the forms that take
// one look up. A pin that already holds something is refused rather than
// replaced.
func (s *Seeding) AddDevice(pin int) {
	s.commands = append(s.commands, cmdFixture+" new "+strconv.Itoa(pin))
}

// Property seeds what a device property reads. This is the world changing
// rather than the program writing, so nothing is recorded.
func (s *Seeding) Property(pin int, property ic10.LogicType, value float64) {
	s.commands = append(s.commands, cmdFixture+" set "+strconv.Itoa(pin)+" "+
		strconv.FormatUint(uint64(property), 10)+" "+formatBits(value))
}

// SlotProperty seeds what a slot property reads. As with [Seeding.Property],
// nothing is recorded.
func (s *Seeding) SlotProperty(pin, slot int, property ic10.LogicSlotType, value float64) {
	s.commands = append(s.commands, cmdFixture+" slot "+strconv.Itoa(pin)+" "+strconv.Itoa(slot)+" "+
		strconv.FormatUint(uint64(property), 10)+" "+formatBits(value))
}

// Hashes gives a device the prefab and name hashes the batch forms select on;
// a device without a prefab hash matches no batch. Unlike the game, this does
// not recompute the name hash on rename — [Seeding.DisplayName] is the paired
// verb that sets both together.
func (s *Seeding) Hashes(pin, prefab, name int) {
	s.commands = append(s.commands, cmdDev+" "+strconv.Itoa(pin)+" hash "+
		strconv.Itoa(prefab)+" "+strconv.Itoa(name))
}

// DisplayName renames a device, which decides where it falls when a batch
// form folds the cable's devices: the game sorts by name and folds backwards,
// so which of two devices a Minimum or Sum answers for depends on the name,
// not add order. The sort is unstable, so devices sharing a name are in no
// defined order, as in the game. This also sets the name hash; use
// [Seeding.Hashes] to set them apart.
func (s *Seeding) DisplayName(pin int, name string) {
	s.commands = append(s.commands, cmdDev+" "+strconv.Itoa(pin)+" name "+encodeText(name))
}

// Reagent seeds what lr reads for one reagent under one mode; see
// [FixtureHarness.SetReagent] for what a mode may be. An invalid mode is kept
// and reported by [FixtureHarness.SeedWorld], so a batch built in a loop
// states the mistake once.
func (s *Seeding) Reagent(pin int, mode ic10.ReagentMode, reagent string, value float64) {
	verb, ok := reagentSeeds[mode]
	if !ok {
		if s.err == nil {
			s.err = fmt.Errorf("%w: no reagent is seeded under reagent mode %d", ErrUnavailable, mode)
		}
		return
	}
	s.commands = append(s.commands, cmdDev+" "+strconv.Itoa(pin)+" "+verb+" "+reagent+" "+formatBits(value))
}

// SeedWorld sends a batch of world changes in one exchange.
func (f *FixtureHarness) SeedWorld(ctx context.Context, s *Seeding) error {
	if s.err != nil {
		return s.err
	}
	return f.do(ctx, s.commands...)
}

// AddDevice puts one recording device on a pin. See [Seeding.AddDevice].
func (f *FixtureHarness) AddDevice(ctx context.Context, pin int) error {
	var world Seeding
	world.AddDevice(pin)
	return f.SeedWorld(ctx, &world)
}

// AddDevices puts recording devices on pins d0 upward, in one exchange.
func (f *FixtureHarness) AddDevices(ctx context.Context, count int) error {
	if count < 0 || count > ic10.NumDevicePins {
		return fmt.Errorf("%w: %d devices is more than the %d pins the housing has",
			ErrUnavailable, count, ic10.NumDevicePins)
	}
	var world Seeding
	for pin := range count {
		world.AddDevice(pin)
	}
	return f.SeedWorld(ctx, &world)
}

// SetProperty seeds one device property. See [Seeding.Property].
func (f *FixtureHarness) SetProperty(ctx context.Context, pin int, property ic10.LogicType, value float64) error {
	var world Seeding
	world.Property(pin, property, value)
	return f.SeedWorld(ctx, &world)
}

// SetProperties seeds one value into many of a device's properties in one
// exchange, so a program reading a property the test case never mentioned
// reads that value instead of a zero.
func (f *FixtureHarness) SetProperties(ctx context.Context, pin int, properties []ic10.LogicType, value float64) error {
	var world Seeding
	for _, property := range properties {
		world.Property(pin, property, value)
	}
	return f.SeedWorld(ctx, &world)
}

// SetSlotProperty seeds one slot property. See [Seeding.SlotProperty].
func (f *FixtureHarness) SetSlotProperty(ctx context.Context, pin, slot int, property ic10.LogicSlotType, value float64) error {
	var world Seeding
	world.SlotProperty(pin, slot, property, value)
	return f.SeedWorld(ctx, &world)
}

// SetHashes gives a device the prefab and name hashes the batch forms select
// on. See [Seeding.Hashes].
func (f *FixtureHarness) SetHashes(ctx context.Context, pin, prefab, name int) error {
	var world Seeding
	world.Hashes(pin, prefab, name)
	return f.SeedWorld(ctx, &world)
}

// SetDisplayName renames a device, which decides where a batch fold reaches it.
// See [Seeding.DisplayName].
func (f *FixtureHarness) SetDisplayName(ctx context.Context, pin int, name string) error {
	var world Seeding
	world.DisplayName(pin, name)
	return f.SeedWorld(ctx, &world)
}

// reagentSeedNames is the harness verb that seeds what each of lr's per-reagent
// modes reads. TotalContents has none: it sums the mixture Contents seeds
// rather than holding a quantity of its own.
var reagentSeedNames = map[string]string{
	"Contents": "mixture",
	"Required": "required",
	"Recipe":   "recipe",
}

// reagentSeeds keys those verbs by the value a program names the mode with,
// derived from the generated table so a game update that renumbers
// ReagentMode reaches it. A mode the table no longer names drops out and is
// refused.
var reagentSeeds = buildReagentSeeds()

func buildReagentSeeds() map[ic10.ReagentMode]string {
	out := make(map[ic10.ReagentMode]string, len(reagentSeedNames))
	for _, mode := range ic10.ReagentModes {
		if verb, ok := reagentSeedNames[mode.Name]; ok {
			out[mode.Value] = verb
		}
	}
	return out
}

// SetReagent seeds what lr reads for one reagent under one mode. The reagent
// is named by the game's short type name, which is what __ic_hash computes
// from a program's reagent hash, so the two agree by construction; an unknown
// name is refused rather than seeded onto nothing.
//
// mode must be one of lr's per-reagent modes; TotalContents is refused since
// it sums the mixture's Contents seeds rather than holding its own quantity.
func (f *FixtureHarness) SetReagent(ctx context.Context, pin int, mode ic10.ReagentMode, reagent string, value float64) error {
	var world Seeding
	world.Reagent(pin, mode, reagent, value)
	return f.SeedWorld(ctx, &world)
}

// Trace is every write the devices have recorded, in program order. A write
// to the housing (db) is never among them, since the housing is reset rather
// than created; that state is in the state block instead. The write count is
// checked against the state block, so a lost or extra write is a protocol
// error, not a short read.
func (f *FixtureHarness) Trace(ctx context.Context) ([]Write, error) {
	snapshot, err := f.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("fixture trace: %w", err)
	}
	if err := f.begin(); err != nil {
		return nil, err
	}
	if err := f.send(cmdFixture + " trace"); err != nil {
		return nil, f.fail(fmt.Errorf("%w: %w", ErrUnavailable, err))
	}
	body, err := f.readBlock(ctx, "fixture trace", snapshot.FixtureWrites)
	if err != nil {
		return nil, err
	}
	if len(body) != snapshot.FixtureWrites {
		return nil, f.fail(fmt.Errorf("%w: fixture trace carried %d writes, and the state block said %d",
			ErrUnavailable, len(body), snapshot.FixtureWrites))
	}

	writes := make([]Write, 0, len(body))
	for _, line := range body {
		write, err := parseWrite(line)
		if err != nil {
			return nil, f.fail(fmt.Errorf("%w: fixture trace: %w", ErrUnavailable, err))
		}
		writes = append(writes, write)
	}
	return writes, nil
}

func parseWrite(line string) (Write, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Write{}, fmt.Errorf("blank line in a trace block")
	}
	var numbers []string
	write := Write{Slot: NoSlot}
	switch fields[0] {
	case "w":
		if len(fields) != 4 {
			return Write{}, fmt.Errorf("a device write wants a pin, a property and a value, got %q", line)
		}
		numbers = []string{fields[1], fields[2]}
	case "ws":
		if len(fields) != 5 {
			return Write{}, fmt.Errorf("a slot write wants a pin, a slot, a property and a value, got %q", line)
		}
		numbers = []string{fields[1], fields[2], fields[3]}
	default:
		return Write{}, fmt.Errorf("unknown trace key %q", fields[0])
	}

	parsed := make([]int, len(numbers))
	for i, text := range numbers {
		value, err := strconv.Atoi(text)
		if err != nil {
			return Write{}, fmt.Errorf("trace line %q: %w", line, err)
		}
		parsed[i] = value
	}
	if err := checkIndices(parsed, line); err != nil {
		return Write{}, err
	}
	write.Pin = parsed[0]
	if len(parsed) == 3 {
		write.Slot, write.Property = parsed[1], parsed[2]
	} else {
		write.Property = parsed[1]
	}

	value, err := parseBits(fields[len(fields)-1])
	if err != nil {
		return Write{}, fmt.Errorf("trace line %q: %w", line, err)
	}
	write.Value = value
	return write, nil
}

// checkIndices refuses a trace line naming a write no device could have made.
// It works over the raw numbers rather than a built [Write], since [NoSlot] is
// itself a negative slot and parsing straight into a Write would turn a
// nonsense slot write into a valid-looking device write.
//
// A pin has both a lower and upper bound; a slot or property has only a lower
// one, since a property is an ordinal a program may name past what the enum
// declares.
func checkIndices(parsed []int, line string) error {
	if pin := parsed[0]; pin < 0 || pin >= ic10.NumDevicePins {
		return fmt.Errorf("trace line %q names pin d%d, and the housing has %d",
			line, pin, ic10.NumDevicePins)
	}
	if len(parsed) == 3 && parsed[1] < 0 {
		return fmt.Errorf("trace line %q names slot %d, and a slot index counts from zero", line, parsed[1])
	}
	if property := parsed[len(parsed)-1]; property < 0 {
		return fmt.Errorf("trace line %q names property %d, and no property has a negative ordinal",
			line, property)
	}
	return nil
}
