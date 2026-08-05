package sema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// expectWarned checks that src analyzes with exactly one diagnostic, that it is
// a warning rather than a rejection, that it points at the marker, and that it
// names want.
func expectWarned(t *testing.T, src, want string) {
	t.Helper()
	pos := markedPos(t, src)
	_, diags := analyze(t, src)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1:\n%s", len(diags), diags.String())
	}
	got := diags[0]
	if got.Severity != source.Warning {
		t.Fatalf("the diagnostic is an %s, and a program the chip may well run must not be rejected: %s", got.Severity, got.Msg)
	}
	if got.Pos.Line != pos.Line || got.Pos.Column != pos.Column {
		t.Errorf("diagnostic at %s, want %s: %s", got.Pos, pos, got.Msg)
	}
	if !strings.Contains(got.Msg, want) {
		t.Errorf("message %q does not name %q", got.Msg, want)
	}
}

// analyzeShipped parses and checks src against the generated roster rather than
// against the stub, so what a test built on it establishes is that the shipped
// table answers the program and not merely that the check can fire.
func analyzeShipped(t *testing.T, src string) source.DiagnosticList {
	t.Helper()
	file, diags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("source did not parse cleanly:\n%s", diags.String())
	}
	_, diags, err = sema.Analyze(context.Background(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return diags
}

// TestBatchAccessWarns covers every batch access the prefab roster contradicts.
//
// The first two carry the shape of the mistakes this compiler's own corpus
// made, as the fixtures wrote them: the prefab hash in a local the declaration
// initialised and nothing wrote to afterwards. The mistakes themselves are
// [TestKnownCorpusMistakesAreReported], which runs them under the names the
// game ships against the roster it ships.
func TestBatchAccessWarns(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "reading a property the device does not answer",
			src: `double bank;
void main(void) {
    long long coolers = __ic_hash("StructureStubCooler");
    bank = __ic_load_batch(coolers, /*!*/Temperature, Average);
}
`,
			want: "a completed StructureStubCooler (Stub Cooler) answers nothing for 'Temperature'",
		},
		{
			name: "writing a property the device does not take",
			src: `void main(void) {
    long long lights = __ic_hash("StructureStubLight");
    __ic_store_batch(lights, /*!*/Setting, 1);
}
`,
			want: "a completed StructureStubLight (Stub Light) accepts no write of 'Setting'",
		},
		{
			name: "the hash written in the operand",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubLight"), /*!*/Setting, 1);
}
`,
			want: "accepts no write of 'Setting'",
		},
		{
			// The batch read faults on a device that refuses the property before
			// the aggregation runs, so counting the devices is no exemption.
			name: "counting devices under a property they do not answer",
			src: `double n;
void main(void) {
    n = __ic_load_batch(__ic_hash("StructureStubCooler"), /*!*/Temperature, Count);
}
`,
			want: "answers nothing for 'Temperature'",
		},
		{
			name: "a thing with no logic surface at all",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("ItemStubTool"), /*!*/On, 1);
}
`,
			want: "a completed ItemStubTool (Stub Tool) accepts no write of 'On'",
		},
		{
			// The game ships some things under no English title, and the
			// diagnostic then has only the roster name to give.
			name: "a thing the game ships no title for",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("ItemStubUntitled"), /*!*/On, 1);
}
`,
			want: "a completed ItemStubUntitled accepts no write of 'On'",
		},
		{
			name: "a name the build ships nothing under",
			src: `void main(void) {
    __ic_store_batch(/*!*/__ic_hash("StructureStubLite"), On, 1);
}
`,
			want: `is given the hash of "StructureStubLite", and this game build ships nothing under that name`,
		},
		{
			name: "a hash the build ships nothing under",
			src: `void main(void) {
    __ic_store_batch(/*!*/12345, On, 1);
}
`,
			want: "is given the prefab hash 12345, and this game build ships nothing under it",
		},
		{
			name: "reading a property the named devices do not answer",
			src: `double bank;
void main(void) {
    bank = __ic_load_batch_named(__ic_hash("StructureStubCooler"), __ic_hash("north"), /*!*/Temperature, Average);
}
`,
			want: "a completed StructureStubCooler (Stub Cooler) answers nothing for 'Temperature'",
		},
		{
			name: "writing a property the named devices do not take",
			src: `void main(void) {
    __ic_store_batch_named(__ic_hash("StructureStubLight"), __ic_hash("north"), /*!*/Setting, 1);
}
`,
			want: "a completed StructureStubLight (Stub Light) accepts no write of 'Setting'",
		},
		{
			// The value a named store writes stands one argument further along
			// than a plain one's, which is where the Mode number is read from.
			name: "a mode number past the settings the named devices have",
			src: `void main(void) {
    __ic_store_batch_named(__ic_hash("StructureStubConsole"), __ic_hash("north"), Mode, /*!*/3);
}
`,
			want: "__ic_store_batch_named writes 3 to 'Mode', and a completed StructureStubConsole (Stub Console) has 3 modes to select between, numbered from 0",
		},
		{
			name: "a slot the device does not declare",
			src: `void main(void) {
    __ic_store_batch_slot(__ic_hash("StructureStubLocker"), /*!*/3, SlotType_Lock, 1);
}
`,
			want: "a completed StructureStubLocker (Stub Locker) declares 1 slot, and __ic_store_batch_slot addresses slot 3",
		},
		{
			name: "a slot property the slot does not take",
			src: `void main(void) {
    __ic_store_batch_slot(__ic_hash("StructureStubFurnace"), 0, /*!*/Occupied, 1);
}
`,
			want: "slot 0 of a completed StructureStubFurnace (Stub Furnace) accepts no write of 'Occupied'",
		},
		{
			name: "a slot property the slot does not answer",
			src: `double held;
void main(void) {
    held = __ic_load_batch_named_slot(__ic_hash("StructureStubFurnace"), __ic_hash("north"), 1, /*!*/SlotType_Lock, Sum);
}
`,
			want: "slot 1 of a completed StructureStubFurnace (Stub Furnace) answers nothing for 'SlotType_Lock'",
		},
		{
			name: "a mode number past the settings the device has",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/3);
}
`,
			want: "a completed StructureStubConsole (Stub Console) has 3 modes to select between, numbered from 0",
		},
		{
			name: "a negative mode number",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/-1);
}
`,
			want: "writes -1 to 'Mode'",
		},
		{
			// The device reaches its mode through a signed integer conversion, so
			// the fraction is dropped rather than rejected and the whole part is
			// what has to be in range.
			name: "a fractional mode number whose whole part is past the settings the device has",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/3.5);
}
`,
			want: "writes 3.5 to 'Mode', which the device converts to mode 3, and a completed StructureStubConsole (Stub Console) has 3 modes to select between, numbered from 0",
		},
		{
			// Nothing settles which mode a magnitude past the top of the range
			// reaches, so the warning names the number written and no mode.
			name: "an infinite mode number",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/1.0e300 * 1.0e300);
}
`,
			want: "writes +Inf to 'Mode', and a completed StructureStubConsole (Stub Console) has 3 modes",
		},
		{
			// A NaN is the other input the conversion is unspecified for, and it
			// cannot be read as selecting the first mode.
			name: "a mode number that is not a number",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/0.0 / 0.0);
}
`,
			want: "writes NaN to 'Mode', and a completed StructureStubConsole (Stub Console) has 3 modes",
		},
		{
			name: "a whole mode number past what the device's own integer holds",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/3000000000);
}
`,
			want: "writes 3000000000 to 'Mode', and a completed StructureStubConsole (Stub Console) has 3 modes",
		},
		{
			// Below the range the two readings of the conversion agree, so this
			// end is the one mode number a warning can still name.
			name: "a mode number past the negative end of that integer",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/-1.0e300);
}
`,
			want: "writes -1e+300 to 'Mode', which the device converts to mode -2147483648",
		},
		{
			name: "a negative fractional mode number",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, /*!*/-1.5);
}
`,
			want: "writes -1.5 to 'Mode', which the device converts to mode -1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectWarned(t, tt.src, tt.want)
		})
	}
}

// TestBatchAccessIsSilent covers what the check must never report. Every entry
// here is a program the chip runs, or one the compiler cannot decide anything
// about, and a warning on any of them is the failure this check exists to
// avoid rather than a nuisance.
func TestBatchAccessIsSilent(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// A device that answers whatever it is pointed at is one the
			// extraction could decide no direction for. A check that read
			// undecided as denied would reject this, which is the wrong-answer
			// bug the roster exists to catch.
			name: "a property whose direction the game settles at run time",
			src: `double reading;
void main(void) {
    long long mirrors = __ic_hash("StructureStubMirror");
    reading = __ic_load_batch(mirrors, Temperature, Average);
    __ic_store_batch(mirrors, Setting, reading);
    __ic_store_batch(mirrors, On, 1);
}
`,
		},
		{
			name: "a property the device answers",
			src: `double bank;
void main(void) {
    long long sensors = __ic_hash("StructureStubSensor");
    bank = __ic_load_batch(sensors, Temperature, Average);
    __ic_store_batch(__ic_hash("StructureStubCooler"), On, bank > 300.0);
}
`,
		},
		{
			// The device a pin is wired to is decided when the chip is placed,
			// so a program that does not say what is on one leaves nothing to
			// check. Both of these are properties a stub entry refuses.
			name: "a pin no declaration names it",
			src: `double reading;
void main(void) {
    reading = __ic_load(d0, Temperature);
    __ic_store(d1, Setting, reading);
    __ic_store_slot(d2, 4, Occupied, 1);
}
`,
		},
		{
			name: "a prefab hash the call site supplies",
			src: `void switchAll(long long prefab) {
    __ic_store_batch(prefab, Setting, 1);
}
void main(void) {
    switchAll(__ic_hash("StructureStubLight"));
}
`,
		},
		{
			name: "a prefab hash a later write replaces",
			src: `void main(void) {
    long long prefab = __ic_hash("StructureStubLight");
    prefab = __ic_hash("StructureStubCooler");
    __ic_store_batch(prefab, Setting, 1);
}
`,
		},
		{
			name: "a prefab hash a compound assignment reaches",
			src: `void main(void) {
    long long prefab = __ic_hash("StructureStubLight");
    prefab += 1;
    __ic_store_batch(prefab, Setting, 1);
}
`,
		},
		{
			// A write through a pointer is a write this pass does not see at
			// all, so an object whose address was taken settles nothing.
			name: "a prefab hash whose object was addressed",
			src: `void bump(long long *p) { *p = *p + 1; }
void main(void) {
    long long prefab = __ic_hash("StructureStubLight");
    bump(&prefab);
    __ic_store_batch(prefab, Setting, 1);
}
`,
		},
		{
			name: "a prefab hash held in an array",
			src: `long long prefabs[2];
void main(void) {
    prefabs[0] = __ic_hash("StructureStubLight");
    __ic_store_batch(prefabs[0], Setting, 1);
}
`,
		},
		{
			// Mode state whose names the extraction could not recover is not
			// the same as none, and the inherited default the game declares is
			// not what such a class ends up with.
			name: "a mode number on a device whose modes are unresolved",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubPanel"), Mode, 9);
}
`,
		},
		{
			name: "a mode number the program computes",
			src: `long long wanted;
void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, wanted);
}
`,
		},
		{
			name: "a mode number the device has",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, 2);
}
`,
		},
		{
			// The conversion truncates toward zero, so both of these select a
			// mode the device has and neither is a mistake to report.
			name: "a fractional mode number whose whole part the device has",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, 2.5);
    __ic_store_batch(__ic_hash("StructureStubConsole"), Mode, -0.5);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestDeclaredPinAccessWarns covers every pin-addressed access the prefab a
// declaration named contradicts.
//
// What a pin reaches is decided when the chip is wired in-game, so each of these
// says only that the access is wrong given what the program promised. That is
// why the promise has to be written down before any of them fires.
func TestDeclaredPinAccessWarns(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "writing a property the declared device does not take",
			src: `[[ic11c::prefab("StructureStubLight")]] const dev lamp = d0;
void main(void) {
    __ic_store(lamp, /*!*/Setting, 1);
}
`,
			want: "a completed StructureStubLight (Stub Light) accepts no write of 'Setting'; this program declares d0 to be a StructureStubLight, and __ic_store writes it to whatever the world put there, so the chip faults on it wherever the declaration holds",
		},
		{
			name: "reading a property the declared device does not answer",
			src: `[[ic11c::prefab("StructureStubSensor")]] const dev sensor = d1;
double lit;
void main(void) {
    lit = __ic_load(sensor, /*!*/On);
}
`,
			want: "a completed StructureStubSensor (Stub Sensor) answers nothing for 'On'; this program declares d1 to be a StructureStubSensor, and __ic_load reads it from whatever the world put there",
		},
		{
			// The declaration promises something about the pin, not about the
			// name, so the bare spelling of the same pin is judged by it too.
			name: "through the bare pin spelling rather than the object",
			src: `[[ic11c::prefab("StructureStubLight")]] const dev lamp = d2;
void main(void) {
    __ic_store(d2, /*!*/Setting, 1);
}
`,
			want: "this program declares d2 to be a StructureStubLight",
		},
		{
			name: "through the housing the chip sits in",
			src: `[[ic11c::prefab("StructureStubLight")]] const dev self = db;
void main(void) {
    __ic_store(self, /*!*/Setting, 1);
}
`,
			want: "this program declares db to be a StructureStubLight",
		},
		{
			// The claim is about the world, which has no scopes, so a
			// declaration in one function decides an access in another.
			name: "declared in a block and reached from elsewhere",
			src: `void tick(void) {
    __ic_store(d0, /*!*/Setting, 1);
}
void main(void) {
    [[ic11c::prefab("StructureStubLight")]] const dev lamp = d0;
    __ic_store(lamp, On, 1);
    tick();
}
`,
			want: "a completed StructureStubLight (Stub Light) accepts no write of 'Setting'",
		},
		{
			name: "a slot the declared device does not declare",
			src: `[[ic11c::prefab("StructureStubLocker")]] const dev store = d0;
void main(void) {
    __ic_store_slot(store, /*!*/3, SlotType_Lock, 1);
}
`,
			want: "a completed StructureStubLocker (Stub Locker) declares 1 slot, and __ic_store_slot addresses slot 3; the chip refuses the slot on the device rather than on the line, so it faults on whatever the world put on d0 wherever this program's declaration of it holds",
		},
		{
			name: "a slot property the declared device's slot does not take",
			src: `[[ic11c::prefab("StructureStubFurnace")]] const dev furnace = d0;
void main(void) {
    __ic_store_slot(furnace, 0, /*!*/Occupied, 1);
}
`,
			want: "slot 0 of a completed StructureStubFurnace (Stub Furnace) accepts no write of 'Occupied'",
		},
		{
			name: "a slot property the declared device's slot does not answer",
			src: `[[ic11c::prefab("StructureStubFurnace")]] const dev furnace = d0;
double held;
void main(void) {
    held = __ic_load_slot(furnace, 1, /*!*/SlotType_Lock);
}
`,
			want: "slot 1 of a completed StructureStubFurnace (Stub Furnace) answers nothing for 'SlotType_Lock'",
		},
		{
			name: "a slot the declared device does not declare, read rather than written",
			src: `[[ic11c::prefab("StructureStubLocker")]] const dev store = d0;
double held;
void main(void) {
    held = __ic_load_slot(store, /*!*/3, Occupied);
}
`,
			want: "a completed StructureStubLocker (Stub Locker) declares 1 slot, and __ic_load_slot addresses slot 3",
		},
		{
			name: "a mode number past the settings the declared device has",
			src: `[[ic11c::prefab("StructureStubConsole")]] const dev console = d0;
void main(void) {
    __ic_store(console, Mode, /*!*/3);
}
`,
			want: "a completed StructureStubConsole (Stub Console) has 3 modes to select between, numbered from 0",
		},
		{
			name: "a fractional mode number past the settings the declared device has",
			src: `[[ic11c::prefab("StructureStubConsole")]] const dev console = d0;
void main(void) {
    __ic_store(console, Mode, /*!*/3.5);
}
`,
			want: "writes 3.5 to 'Mode', which the device converts to mode 3, and a completed StructureStubConsole (Stub Console) has 3 modes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectWarned(t, tt.src, tt.want)
		})
	}
}

// TestPinAccessStaysSilent covers everything a pin-addressed access leaves
// undecided, all of which the compiler has to say nothing about.
//
// The first is the one that matters most: adding the check must not make a
// program that says nothing about its pins any noisier than it was, and that is
// almost every program.
func TestPinAccessStaysSilent(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a pin no declaration names",
			src: `void main(void) {
    __ic_store(d0, Setting, 1);
    __ic_store_slot(d1, 7, Occupied, 1);
}
`,
		},
		{
			name: "a property the declared device answers",
			src: `[[ic11c::prefab("StructureStubSensor")]] const dev sensor = d0;
double p;
void main(void) {
    p = __ic_load(sensor, Pressure);
}
`,
		},
		{
			// A device the game settles from live state leaves the extraction
			// nothing to record, and an undecided surface refuses nothing.
			name: "a property the extraction left undecided",
			src: `[[ic11c::prefab("StructureStubMirror")]] const dev mirror = d0;
double reading;
void main(void) {
    __ic_store(mirror, Setting, 1);
    reading = __ic_load(mirror, Temperature);
}
`,
		},
		{
			// Which pin the body reaches is the call site's, and this pass does
			// not follow a call.
			name: "an access through a dev parameter",
			src: `[[ic11c::prefab("StructureStubLight")]] const dev lamp = d0;
void drive(dev target) {
    __ic_store(target, Setting, 1);
}
void main(void) {
    drive(lamp);
}
`,
		},
		{
			name: "a declaration nothing reaches through",
			src: `[[ic11c::prefab("StructureStubLight")]] const dev lamp = d0;
void main(void) {
    __ic_store(d1, Setting, 1);
}
`,
		},
		{
			name: "a fractional mode number the declared device has",
			src: `[[ic11c::prefab("StructureStubConsole")]] const dev console = d0;
void main(void) {
    __ic_store(console, Mode, 2.5);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}

// TestSlotAccessNothingResolvesIsReportedOnce covers an access whose slot index
// is not a constant expression, which is rejected as an operand before the
// roster sees it.
//
// Nothing then says which slot the access reaches, and the zero an unresolved
// operand carries is not one the program named. Judging it as slot 0 would add a
// second verdict about a slot the source does not mention, which the declaration
// here would produce: the locker's only slot answers 'Occupied' and takes no
// write of it.
func TestSlotAccessNothingResolvesIsReportedOnce(t *testing.T) {
	expectRejected(t, `[[ic11c::prefab("StructureStubLocker")]] const dev store = d0;
void main(void) {
    long long which = 0;
    __ic_store_slot(store, /*!*/which, Occupied, 1);
}
`, "the slot index of __ic_store_slot must be a constant expression")
}

// TestPrefabDeclarationIsHeldToTheRoster covers what the compiler can decide
// about a declaration itself, rather than about an access through it.
func TestPrefabDeclarationIsHeldToTheRoster(t *testing.T) {
	t.Run("a name the build ships nothing under", func(t *testing.T) {
		// The name is a fact about the roster, so it is reported even though
		// nothing reads through the pin. It stays a warning for the reason every
		// roster verdict does: the roster is one pinned build.
		expectWarned(t, `[[ic11c::prefab(/*!*/"StructureStubLite")]] const dev lamp = d0;
void main(void) {
    __ic_store(lamp, Setting, 1);
}
`, `this game build ships nothing named "StructureStubLite", so nothing is known about what 'd0' reaches and no access through it is checked`)
	})

	t.Run("one pin declared as two prefabs", func(t *testing.T) {
		expectRejected(t, `[[ic11c::prefab("StructureStubLight")]] const dev lamp = d0;
[[ic11c::prefab(/*!*/"StructureStubSensor")]] const dev sensor = d0;
void main(void) {
    __ic_store(lamp, On, 1);
}
`, "'d0' is declared to be a StructureStubLight at test.c:1:17, and one housing position reaches one device")
	})

	t.Run("one pin declared twice as the same prefab", func(t *testing.T) {
		expectAccepted(t, `[[ic11c::prefab("StructureStubLight")]] const dev lamp = d0;
void main(void) {
    [[ic11c::prefab("StructureStubLight")]] const dev again = d0;
    __ic_store(again, On, 1);
}
`)
	})

	t.Run("db declared to be a thing that holds no chip", func(t *testing.T) {
		// The program is running inside whatever db names, so a thing with
		// nowhere to put a chip is a claim the roster settles on its own. It is
		// reported at the declaration, since no access has to reach through it
		// for the contradiction to exist.
		expectWarned(t, `[[ic11c::prefab(/*!*/"StructureStubSensor")]] constexpr dev self = db;
void main(void) {
    __ic_store(d0, On, 1);
}
`, "'db' is the housing this chip is inserted into, and a completed StructureStubSensor (Stub Sensor) holds no programmable chip; the game cannot place this chip in one, so nothing this declaration says can be true of the housing the program is running in")
	})

	t.Run("db declared to be a thing that holds a chip", func(t *testing.T) {
		expectAccepted(t, `[[ic11c::prefab("StructureStubHousing")]] constexpr dev self = db;
void main(void) {
    __ic_store(self, Setting, 1);
}
`)
	})

	t.Run("db declared to be a thing the roster leaves undecided", func(t *testing.T) {
		// Whether the thing holds a chip is exactly what the extraction failed
		// to recover, and a denial there would report a thing as not being what
		// it is.
		expectAccepted(t, `[[ic11c::prefab("StructureStubLight")]] constexpr dev self = db;
void main(void) {
    __ic_store(self, On, 1);
}
`)
	})

	t.Run("a pin declared to be a thing that holds no chip", func(t *testing.T) {
		// A pin reaches something else in the world, so what is wired to it has
		// no reason to hold a chip and there is nothing to report.
		expectAccepted(t, `[[ic11c::prefab("StructureStubSensor")]] const dev sensor = d0;
void main(void) {
    __ic_store(d1, On, 1);
}
`)
	})

	t.Run("a declaration that names no device", func(t *testing.T) {
		expectRejected(t, `/*!*/[[ic11c::prefab("StructureStubLight")]] const long long lamp = 3;
void main(void) {
    __ic_store(d0, On, lamp);
}
`, "'lamp' has type const long long, and the prefab attribute states which device a housing position is wired to; it belongs on a dev declaration")
	})
}

// TestDeprecatedPropertyIsNotADeviceSurfaceWarning covers a property the game
// still answers and has also marked retired. The deprecation is worth saying
// and the device surface is not: a device that answers the property answers it
// whether or not the game has moved on.
func TestDeprecatedPropertyIsNotADeviceSurfaceWarning(t *testing.T) {
	const src = `double health;
void main(void) {
    health = __ic_load_batch(__ic_hash("StructureStubPlanter"), PlantHealth1, BatchMode_Minimum);
}
`
	_, diags := analyze(t, src)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want only the deprecation warning:\n%s", len(diags), diags.String())
	}
	if !strings.Contains(diags[0].Msg, "deprecated") {
		t.Errorf("the one diagnostic is not the deprecation warning: %s", diags[0].Msg)
	}
}

// TestKnownCorpusMistakesAreReported runs the two mistakes this compiler's own
// corpus carried against the generated roster rather than against the stub, so
// what is proved is that the shipped table answers them and not merely that the
// check can fire.
//
// Both were written pin-addressed as well as batch-addressed, and both halves
// are here. The pin halves are checked only because the program says what the
// pin is wired to; without that declaration finding them took a third-party
// database of a different game build.
func TestKnownCorpusMistakesAreReported(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// cooler_bank.c read the ceiling temperature off the coolers it was
			// switching. A wall cooler answers TemperatureOutput and no
			// Temperature of its own.
			name: "cooler_bank.c reading Temperature from a wall cooler",
			src: `double bank;
void main(void) {
    long long coolers = __ic_hash("StructureWallCooler");
    bank = __ic_load_batch(coolers, Temperature, Average);
}
`,
			want: "a completed StructureWallCooler (Wall Cooler) answers nothing for 'Temperature'",
		},
		{
			// bits.c switched a bank of wall lights through Setting. A wall
			// light's switch is On.
			name: "bits.c writing Setting to a wall light",
			src: `void main(void) {
    long long lights = __ic_hash("StructureWallLight");
    __ic_store_batch(lights, Setting, 1);
}
`,
			want: "a completed StructureWallLight (Wall Light) accepts no write of 'Setting'",
		},
		{
			name: "cooler_bank.c reading Temperature through a pin it declares",
			src: `[[ic11c::prefab("StructureWallCooler")]] const dev cooler = d0;
double ceiling;
void main(void) {
    ceiling = __ic_load(cooler, Temperature);
}
`,
			want: "a completed StructureWallCooler (Wall Cooler) answers nothing for 'Temperature'; this program declares d0 to be a StructureWallCooler, and __ic_load reads it from whatever the world put there",
		},
		{
			name: "bits.c writing Setting through a pin it declares",
			src: `[[ic11c::prefab("StructureWallLight")]] const dev lamp = d1;
void main(void) {
    __ic_store(lamp, Setting, 1);
}
`,
			want: "a completed StructureWallLight (Wall Light) accepts no write of 'Setting'; this program declares d1 to be a StructureWallLight, and __ic_store writes it to whatever the world put there",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := analyzeShipped(t, tt.src)
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want exactly 1:\n%s", len(diags), diags.String())
			}
			if diags[0].Severity != source.Warning {
				t.Fatalf("the mistake was reported as an %s, and a program the chip may well run must not be rejected", diags[0].Severity)
			}
			if !strings.Contains(diags[0].Msg, tt.want) {
				t.Errorf("message %q does not name %q", diags[0].Msg, tt.want)
			}
		})
	}
}

// TestModeNumberIsHeldToTheShippedRoster runs the mode check against the
// generated roster rather than against the stub.
//
// The mode counts the stub carries are its own invention, so on their own they
// establish only that the check fires when a roster answers one. This is what
// establishes that the shipped roster answers one at all: an extraction that
// recovered no mode names anywhere would leave every mode check silent and
// every stub test still passing.
func TestModeNumberIsHeldToTheShippedRoster(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "one past the last mode",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("CompositeRollCover"), Mode, 2);
}
`,
			want: "a completed CompositeRollCover (Composite Roll Cover) has 2 modes to select between, numbered from 0",
		},
		{
			name: "a fractional mode number whose whole part is past the last mode",
			src: `void main(void) {
    __ic_store_batch(__ic_hash("CompositeRollCover"), Mode, 2.5);
}
`,
			want: "writes 2.5 to 'Mode', which the device converts to mode 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := analyzeShipped(t, tt.src)
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want exactly 1:\n%s", len(diags), diags.String())
			}
			if diags[0].Severity != source.Warning {
				t.Fatalf("the mode number was reported as an %s, and the roster describes one pinned build", diags[0].Severity)
			}
			if !strings.Contains(diags[0].Msg, tt.want) {
				t.Errorf("message %q does not name %q", diags[0].Msg, tt.want)
			}
		})
	}
}

// TestHousingDeclarationIsHeldToTheShippedRoster covers the one thing a
// declaration of db says that the roster can decide on its own, against the
// generated table rather than the stub.
//
// A declaration of a pin is a promise about how the chip was wired, which
// nothing can check. A declaration of db is a promise about the chip's own
// housing, and a thing with nowhere to put a chip cannot be it.
func TestHousingDeclarationIsHeldToTheShippedRoster(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want is a fragment of the one warning, and is empty for a program the
		// roster leaves nothing to say about.
		want string
	}{
		{
			name: "a thing the roster says holds no chip",
			src: `[[ic11c::prefab("StructureGasSensor")]] constexpr dev self = db;
void main(void) {
    __ic_store(d0, On, 1);
}
`,
			want: "a completed StructureGasSensor (Gas Sensor) holds no programmable chip",
		},
		{
			name: "the housing a chip is placed in",
			src: `[[ic11c::prefab("StructureCircuitHousing")]] constexpr dev self = db;
void main(void) {
    __ic_store(d0, On, 1);
}
`,
		},
		{
			// A program that says nothing about its housing is almost every
			// program, and has to compile exactly as it did before.
			name: "a housing no declaration names",
			src: `double p;
void main(void) {
    p = __ic_load(db, Setting);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := analyzeShipped(t, tt.src)
			if tt.want == "" {
				if len(diags) != 0 {
					t.Fatalf("got %d diagnostics, want none:\n%s", len(diags), diags.String())
				}
				return
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want exactly 1:\n%s", len(diags), diags.String())
			}
			if diags[0].Severity != source.Warning {
				t.Fatalf("the declaration was reported as an %s, and the roster describes one pinned build", diags[0].Severity)
			}
			if !strings.Contains(diags[0].Msg, tt.want) {
				t.Errorf("message %q does not name %q", diags[0].Msg, tt.want)
			}
		})
	}
}

// TestUndeclaredPinHalvesOfTheCorpusMistakesStaySilent is the other half of
// what [TestKnownCorpusMistakesAreReported] establishes: the same two accesses,
// through the same pins, with nothing said about what is on them.
//
// It runs against the generated roster rather than the stub, so what it
// establishes is that the shipped table stays silent here and not merely that
// the stub does. Silence is the whole point: the declaration is what makes the
// access checkable, and a program that writes none must compile exactly as it
// did before this check existed.
func TestUndeclaredPinHalvesOfTheCorpusMistakesStaySilent(t *testing.T) {
	const src = `double ceiling;
void main(void) {
    ceiling = __ic_load(d0, Temperature);
    __ic_store(d1, Setting, 1);
}
`
	if diags := analyzeShipped(t, src); len(diags) != 0 {
		t.Errorf("an undeclared pin produced %d diagnostics, want none:\n%s", len(diags), diags.String())
	}
}

// TestBatchAccessSurvivesAnUnresolvedProperty covers the interaction between
// this check and the one above it: a property name that resolved to nothing is
// reported once, by the operand check, and this pass adds nothing to it.
func TestBatchAccessSurvivesAnUnresolvedProperty(t *testing.T) {
	const src = `void main(void) {
    __ic_store_batch(__ic_hash("StructureStubLight"), /*!*/NotAProperty, 1);
}
`
	expectRejected(t, src, "is not a logic type")
}
