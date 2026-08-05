package main

import (
	"fmt"
	"strings"
)

const (
	housingPath       = "Assets/Scripts/Objects/Electrical/CircuitHousing.cs"
	logicUnitBasePath = "Assets/Scripts/Objects/Electrical/LogicUnitBase.cs"
	smallDevicePath   = "Assets/Scripts/Objects/SmallDevice.cs"
)

// classLink is one class in a chain an emitted type collapses, with the base it
// must be declared with.
type classLink struct{ path, name, base string }

// housingChain is the class chain CircuitHousing reaches Device through.
// The emitted housing derives from the device shim directly — sound only
// while the two classes in between leave the logic surface alone, since a
// lifted base call has to mean Device's body and nothing else.
var housingChain = []classLink{
	{housingPath, "CircuitHousing", "LogicUnitBase"},
	{logicUnitBasePath, "LogicUnitBase", "SmallDevice"},
	{smallDevicePath, "SmallDevice", "Device"},
}

// housingAccessors are the members whose appearance in a class between
// CircuitHousing and Device would change what a lifted base call dispatches to.
var housingAccessors = []string{
	"CanLogicRead(",
	"CanLogicWrite(",
	"GetLogicValue(",
	"SetLogicValue(",
	"GetSlot(",
}

// housingLifts is every member of CircuitHousing the emitted unit takes as
// written: the whole of its logic surface — which reads and writes it
// permits, where a device code routes, what raised errors become. Two
// members are hand-written below instead, each because its body is world simulation.
var housingLifts = []memberLift{
	{signature: "public const int RUN_COUNT",
		note: "the instruction budget one tick spends, which is what a caller reproducing\n" +
			"the game's own segmentation steps the chip with"},
	{signature: "public ILogicable[] Devices", note: "the pin array, set directly rather than found on the power network"},
	{signature: "private string[] _DeviceLabels"},
	{signature: "private bool _hasPut"},
	{signature: "private int _codeErrorState"},
	{signature: "private byte _processingUpdateFlags"},

	{signature: "public static readonly string[] SettingDisplayModeStrings"},
	{signature: "public override string[] ModeStrings",
		note: "the housing's mode count, and so what a Mode write to db is clamped into"},

	{signature: "public void HasPut()"},
	{signature: "public void ClearError()"},
	{signature: "public void SetDeviceLabel(int index, string label)"},
	{signature: "protected override bool IsOperable"},
	{signature: "public void RaiseError(int state)"},
	{signature: "public void RefreshError()",
		note: "what connects a raised error to the Error a program reads back off db"},

	{signature: "public ILogicable GetLogicableFromIndex(int deviceIndex, int networkIndex = int.MinValue)",
		note: "the int.MaxValue arm is what makes db the housing itself"},
	{signature: "public bool IsValidIndex(int index)"},
	{signature: "public List<ILogicable> GetBatchOutput()",
		note: "what every batch form resolves: a housing on no data cable answers null, which is\n" +
			"the DeviceListNull fault, and one on a cable answers that cable's devices sorted"},

	{signature: "public override bool CanLogicRead(LogicType logicType)"},
	{signature: "public override bool CanLogicWrite(LogicType logicType)"},
	{signature: "public override double GetLogicValue(LogicType logicType)"},
	{signature: "public override void SetLogicValue(LogicType logicType, double value)"},
	{signature: "public override bool CanLogicRead(LogicSlotType logicSlotType, int slotId)"},
	{signature: "public override double GetLogicValue(LogicSlotType logicSlotType, int slotId)"},

	{signature: "public double ReadMemory(int address)"},
	{signature: "public void WriteMemory(int address, double value)"},
	{signature: "public void ClearMemory()"},
	{signature: "public int GetStackSize()"},
}

// housingEdits are the only tokens that change across all of the bodies
// above: the batch list redirected onto a live sort (the shim has no
// connect events to invalidate a cache, so it sorts every call instead),
// three base-qualified names the shim now declares directly, and one Unity lifetime check turned into a plain null test.
var housingEdits = []textEdit{
	{old: "base.InputNetwork1DevicesSorted", new: "Logicable.RecalculateSortedDevicesList(InputNetwork1)", count: 1},
	{old: "base.InputNetwork1", new: "InputNetwork1"},
	{old: "base.NetworkUpdateFlags", new: "NetworkUpdateFlags"},
	{old: "base.InteractError", new: "InteractError"},
	{old: "(Object)(object)ProgrammableChip != (Object)null", new: "ProgrammableChip != null"},
}

// sliceHousing renders the housing shim: the game's own routing and permissions
// over pins, a chip and a setting the harness supplies.
func sliceHousing(s *slicing) (string, error) {
	if err := checkChain(s, housingChain, housingAccessors); err != nil {
		return "", err
	}
	src, err := s.read(housingPath)
	if err != nil {
		return "", err
	}
	housing, err := src.topLevelType("CircuitHousing")
	if err != nil {
		return "", err
	}

	body := src.top().scopeOf(housing)
	made := make(map[string]int, len(housingEdits))
	parts := make([]string, 0, len(housingLifts)+2)
	parts = append(parts, housingInjectedState)
	for _, want := range housingLifts {
		m, err := body.member(want.signature)
		if err != nil {
			return "", fmt.Errorf("%s: CircuitHousing.%w", src.rel, err)
		}
		text, err := applyEdits(s.strip(m.text), housingEdits, made,
			src.rel+": CircuitHousing."+want.signature)
		if err != nil {
			return "", err
		}
		parts = append(parts, indent(provenance(body.span(m), want.note)+"\n"+dedent(text)))
	}
	if err := checkEditCounts(housingEdits, made, src.rel+": CircuitHousing"); err != nil {
		return "", err
	}
	parts = append(parts, housingUnlifted)
	return housingHeader + strings.Join(parts, "\n\n") + "\n}", nil
}

// checkChain refuses a decompile in which an emitted class would no longer
// dispatch the way the game's does: a moved base list, or a class between
// the chain's two ends declaring a member the collapse assumes it leaves
// alone. The first link may declare whatever it likes; every link after it must not.
func checkChain(s *slicing, chain []classLink, members []string) error {
	for i, link := range chain {
		src, err := s.read(link.path)
		if err != nil {
			return err
		}
		typeDecl, err := src.topLevelType(link.name)
		if err != nil {
			return err
		}
		if len(typeDecl.bases) == 0 || typeDecl.bases[0] != link.base {
			return fmt.Errorf("%s: %s derives from %q, expected it to start with %s",
				src.rel, link.name, strings.Join(typeDecl.bases, ", "), link.base)
		}
		if i == 0 {
			continue
		}
		decls, err := splitDecls(src.top().scopeOf(typeDecl).body)
		if err != nil {
			return fmt.Errorf("%s: split %s body: %w", src.rel, link.name, err)
		}
		for _, d := range decls {
			for _, member := range members {
				if strings.Contains(d.name, member) {
					return fmt.Errorf("%s: %s declares %q, which the emitted %s would reach instead of %s's",
						src.rel, link.name, d.name, chain[0].name, chain[len(chain)-1].base)
				}
			}
		}
	}
	return nil
}

const housingHeader = "// " + housingPath + `, collapsed onto the device shim. The game reaches
// Device through LogicUnitBase and SmallDevice, neither of which declares a
// logic accessor, so deriving from the shim directly is the same dispatch
// with two classes of world object left out.
public class CircuitHousing : Device, ICircuitHolder, IMemoryReadable, IMemory, IMemoryWritable
{
`

// housingInjectedState is the state the collapsed classes and the world carried.
const housingInjectedState = `	// Injected: what the housing is wired to. In the game the chip sits in the
	// housing's first inventory slot; the setting is LogicUnitBase's own
	// property, whose setter only raises events and a network flag. The input
	// network is null until a run wires a cable; unwired, a pin lookup filters against nothing and every batch form faults.
	public ProgrammableChip ProgrammableChip;

	public double Setting;

	public CableNetwork InputNetwork1;

	public Dictionary<int, ILogicable> ById = new Dictionary<int, ILogicable>();

	public MemoryLight _MemoryLight = new MemoryLight();

	private int NetworkUpdateFlags;

	// Injected: read-only views onto state the game reads through a tooltip or
	// a once-a-second update. HarnessCodeErrorState is what RaiseError
	// records, which is not the same as LogicType.Error: RefreshError turns it
	// into one, but so does a compilation error the chip is still carrying.
	public int HarnessCodeErrorState
	{
		get { return _codeErrorState; }
	}

	public bool HarnessHasPut
	{
		get { return _hasPut; }
	}`

// housingUnlifted is the one member of the housing whose body is world
// simulation, narrowed to the seam a run sets by hand.
const housingUnlifted = `	// Not lifted: the game finds the device in the global reference registry,
	// filters it against the housing's data network, and answers a cable
	// network when the code named one. CableNetwork's own logic surface is not lifted.
	public ILogicable GetLogicableFromId(int deviceId, int networkIndex = int.MinValue)
	{
		ILogicable found;
		return ById.TryGetValue(deviceId, out found) ? found : null;
	}`
