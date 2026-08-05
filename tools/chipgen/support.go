package main

import (
	"fmt"
	"strings"
)

// wholeType is a game type the unit lifts entire, because everything it
// declares is reachable and nothing it declares needs the game.
type wholeType struct {
	path string
	name string
	// note says why the type is here, when that is not obvious from the name.
	note string
}

// wholeTypes are the types copied over unchanged. Each is read through
// topLevelType, so a second declaration or a rename stops the slice instead
// of contributing a half-understood definition.
var wholeTypes = []wholeType{
	{path: "Assets/Scripts/Objects/Electrical/ScriptCommand.cs", name: "ScriptCommand",
		note: "the opcode roster; ordinals decide nothing but the parse switch reads the names"},
	{path: "Assets/Scripts/Objects/Electrical/InstructionInclude.cs", name: "InstructionInclude",
		note: "the operand-shape mask each variable parser is constructed with"},
	{path: "Assets/Scripts/Objects/Electrical/ProgrammableChipException.cs", name: "ProgrammableChipException",
		note: "carries ICExceptionType, which is what a conformance run compares against"},
	{path: "Assets/Scripts/Objects/Electrical/StackUnderflowException.cs", name: "StackUnderflowException"},
	{path: "Assets/Scripts/Objects/Electrical/AsciiString.cs", name: "AsciiString",
		note: "SetSourceCode stores the program as one, and its length is what the editor budgets"},
	{path: "Assets/Scripts/Objects/Electrical/LogicBatchMethod.cs", name: "LogicBatchMethod"},
	{path: "Assets/Scripts/Objects/Electrical/LogicReagentMode.cs", name: "LogicReagentMode"},
	{path: "Assets/Scripts/Objects/Motherboards/LogicType.cs", name: "LogicType"},
	{path: "Assets/Scripts/Objects/Motherboards/LogicSlotType.cs", name: "LogicSlotType"},
	{path: "Assets/Scripts/Objects/SortingClass.cs", name: "SortingClass",
		note: "the bucket the SortingClass slot arm answers the ordinal of"},
	{path: "Assets/Scripts/Objects/Electrical/LogicMemoryState.cs", name: "LogicMemoryState"},
	{path: "Assets/Scripts/GridSystem/GameState.cs", name: "GameState",
		note: "read by the device permission bodies, which refuse a write while the world is not running"},
	{path: "Assets/Scripts/Objects/Electrical/SettingDisplayMode.cs", name: "SettingDisplayMode",
		note: "the housing's own mode roster; its member count is what a Mode write is clamped into"},

	{path: "Assets/Scripts/Objects/Electrical/IMemory.cs", name: "IMemory"},
	{path: "Assets/Scripts/Objects/Pipes/IMemoryReadable.cs", name: "IMemoryReadable"},
	{path: "Assets/Scripts/Objects/Pipes/IMemoryWritable.cs", name: "IMemoryWritable"},
	{path: "Assets/Scripts/Objects/Electrical/IRequireReagent.cs", name: "IRequireReagent",
		note: "the interface the reagent user answers, which two of lr's modes and rmap cast to"},
}

// logicableLifts is the sort every batch form resolves through; the rest of
// Logicable is player interaction surface the chip never reaches.
var logicableLifts = []memberLift{
	{container: "Logicable",
		signature: "public static List<ILogicable> RecalculateSortedDevicesList(CableNetwork cableNetwork)",
		note:      "the key a batch fold walks in, and the null a housing on no data cable answers"},
}

const logicablePath = "Assets/Scripts/Objects/Pipes/Logicable.cs"

// memberLift is one member taken out of a type the unit does not lift whole.
type memberLift struct {
	path      string
	container string
	signature string
	note      string
}

// localizationLifts are the preprocessing helpers SetSourceCode runs a program
// through before the tokenizer sees it, plus the result type they return. The
// rest of Localization is a string table read off disk and not on this path.
var localizationLifts = []memberLift{
	{container: "Localization", signature: "RegexResult"},
	{container: "Localization", signature: "public static RegexResult GetMatchesForStringPreprocessing(ref string masterString)"},
	{container: "Localization", signature: "public static RegexResult GetMatchesForHashPreprocessing(ref string masterString)"},
	{container: "Localization", signature: "public static RegexResult GetMatchesForBinaryPreprocessing(ref string masterString)"},
	{container: "Localization", signature: "public static RegexResult GetMatchesForHexPreprocessing(ref string masterString)"},
}

const localizationPath = "Assets/Scripts/Localization.cs"

// regexNames are the patterns the preprocessing helpers above compile, taken
// as statements out of Regexes' static constructor rather than as
// declarations, since most of that constructor builds patterns from types not
// lifted here.
var regexNames = []string{
	"CommentLite",
	"PreprocessStrings",
	"PreprocessHashes",
	"PreprocessBinary",
	"PreprocessHex",
}

const regexesPath = "Assets/Scripts/Util/Regexes.cs"

// mathLifts are the two RocketMath entry points the chip reaches: lerp is an
// instruction, and the constant table's description formatter asks whether a
// value is close to whole.
var mathLifts = []memberLift{
	{path: "Assets/Scripts/Util/RocketMath.cs", container: "RocketMath",
		signature: "public static double Lerp(double a, double b, double t)"},
	{path: "Assets/Scripts/Util/RocketMath.cs", container: "RocketMath",
		signature: "public static bool Approximately(double float1, double float2, double tol = 1E-07)"},
}

// extensionLifts holds the tokenizer's drop helper and the IComparable clamp
// two SetLogicValue arms narrow with. Ordering by IComparable sorts NaN below
// every number; Mathf.Clamp below orders by < and > instead, letting NaN pass
// through — both are reachable from one instruction and not interchangeable.
var extensionLifts = []memberLift{
	{path: "Assets/Scripts/Util/ExtensionMethods.cs", container: "ExtensionMethods",
		signature: "public static T[] RemoveAt<T>(this T[] source, int index)"},
	{path: "Assets/Scripts/Util/ExtensionMethods.cs", container: "ExtensionMethods",
		signature: "public static T Clamp<T>(this T value, T min, T max) where T : IComparable<T>"},
}

// gameManagerLifts are the two colour decisions Device.SetLogicValue's Color
// arm makes: how many colours there are to clamp into, and whether the one
// asked for may be selected by logic at all.
var gameManagerLifts = []memberLift{
	{container: "GameManager", signature: "public static bool RunSimulation"},
	{container: "GameManager", signature: "public static int ColorCount"},
	{container: "GameManager", signature: "public static bool IsLogicSelectableColor(int colorIndex)"},
}

const gameManagerPath = "Assets/Scripts/GameManager.cs"

// chemistryLifts is the gas roster a filter's FilterType names, taken out of
// the class that declares it because the rest of that class is the atmospheric
// simulation.
var chemistryLifts = []memberLift{
	{container: "Chemistry", signature: "GasType"},
}

const chemistryPath = "Assets/Scripts/Atmospherics/Chemistry.cs"

// gameManagerSingleton is the only token that changes in those bodies. There is
// one GameManager in the game and the shim is it, so the instance lookup
// resolves to the class's own field.
const gameManagerSingleton = "Singleton<GameManager>.Instance."

// liftMembers reads the named members out of one file's single top-level type.
func liftMembers(s *slicing, lifts []memberLift, path string) ([]string, error) {
	src, err := s.read(path)
	if err != nil {
		return nil, err
	}
	container := lifts[0].container
	typeDecl, err := src.topLevelType(container)
	if err != nil {
		return nil, err
	}
	body := src.top().scopeOf(typeDecl)
	out := make([]string, 0, len(lifts))
	for _, want := range lifts {
		m, err := body.member(want.signature)
		if err != nil {
			return nil, fmt.Errorf("%s: %s.%w", src.rel, container, err)
		}
		out = append(out, indent(s.strip(body.lift(m, want.note))))
	}
	return out, nil
}

// liftRegexLiterals reads the five pattern literals out of Regexes' static
// constructor and redeclares them as fields; only the left-hand side gets a
// declaration, so the pattern strings themselves are the game's own text.
func liftRegexLiterals(s *slicing) ([]string, error) {
	src, err := s.read(regexesPath)
	if err != nil {
		return nil, err
	}
	typeDecl, err := src.topLevelType("Regexes")
	if err != nil {
		return nil, err
	}
	ctor, err := src.top().scopeOf(typeDecl).member("static Regexes()")
	if err != nil {
		return nil, fmt.Errorf("%s: Regexes.%w", src.rel, err)
	}
	out := make([]string, 0, len(regexNames))
	for _, name := range regexNames {
		statement, err := statementFor(ctor.text, name+" = ")
		if err != nil {
			return nil, fmt.Errorf("%s: Regexes static constructor: %w", src.rel, err)
		}
		out = append(out, "\tpublic static readonly Regex "+statement)
	}
	return out, nil
}

// statementFor returns the single statement beginning with prefix, trimmed of
// its indentation, including its terminating semicolon.
func statementFor(src, prefix string) (string, error) {
	start := strings.Index(src, prefix)
	if start < 0 {
		return "", fmt.Errorf("statement %q: %w", prefix, errNotFound)
	}
	if strings.Contains(src[start+len(prefix):], prefix) {
		return "", fmt.Errorf("statement %q appears more than once", prefix)
	}
	depth := 0
	for i := start; i < len(src); i++ {
		if j := skipLiteral(src, i); j != i {
			i = j - 1
			continue
		}
		switch src[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ';':
			if depth == 0 {
				return src[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("statement %q is unterminated", prefix)
}

// sliceGameManager renders the game's three static decisions the operation and
// device bodies read, over the clock, the world state and the colour palette a
// run supplies.
func sliceGameManager(s *slicing) (string, error) {
	lifts, err := liftMembers(s, gameManagerLifts, gameManagerPath)
	if err != nil {
		return "", err
	}
	for i, text := range lifts {
		count := strings.Count(text, gameManagerSingleton)
		if count == 0 {
			continue
		}
		if lifts[i], err = replaceExactly(text, gameManagerSingleton, "", count,
			gameManagerPath+": GameManager."+gameManagerLifts[i].signature); err != nil {
			return "", err
		}
	}
	return gameManagerShim + strings.Join(lifts, "\n\n") + "\n}", nil
}

// gameManagerShim is the state those three read: a clock driven by the frame
// loop, a world state the session sets, and a colour palette loaded off disk.
// An empty palette refuses every logic colour write.
const gameManagerShim = `public static class GameManager
{
	// Injected: the clock _SLEEP_Operation counts down against. The game
	// advances it from the frame loop; here, a reading is what advances it,
	// since Execute has no moment between two readings to move a field in.
	// A step of zero is a clock that never expires.
	private static float _gameTime;

	private static float _gameTimeStep;

	public static float GameTime
	{
		get
		{
			_gameTime += _gameTimeStep;
			return _gameTime;
		}
	}

	public static void SetClock(float now, float step)
	{
		_gameTime = now;
		_gameTimeStep = step;
	}

	public static GameState GameState = GameState.Running;

	public static List<ColorSwatch> CustomColors = new List<ColorSwatch>();

`

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "\t" + line
		}
	}
	return strings.Join(lines, "\n")
}

// sliceSupport renders every type the chip needs that is not the chip itself.
// The interfaces are hand-written, narrowed to only the members the operation
// bodies and batch reducers call, because in the game they extend the
// MonoBehaviour graph and cannot be lifted whole.
func sliceSupport(s *slicing) (string, error) {
	var parts []string

	for _, want := range wholeTypes {
		src, err := s.read(want.path)
		if err != nil {
			return "", err
		}
		typeDecl, err := src.topLevelType(want.name)
		if err != nil {
			return "", err
		}
		// The shape from topLevelType names members, not values; these types
		// are copied whole because an enum's ordinals are data a shape cannot see.
		if err := src.top().cutTree(typeDecl); err != nil {
			return "", err
		}
		parts = append(parts, s.strip(src.top().lift(typeDecl, want.note)))
	}

	regexes, err := liftRegexLiterals(s)
	if err != nil {
		return "", err
	}
	parts = append(parts, "// "+regexesPath+", the five patterns the preprocessor compiles\npublic static class Regexes\n{\n"+
		strings.Join(regexes, "\n\n")+"\n}")

	localization, err := liftMembers(s, localizationLifts, localizationPath)
	if err != nil {
		return "", err
	}
	parts = append(parts, "public static class Localization\n{\n"+strings.Join(localization, "\n\n")+"\n}")

	math, err := liftMembers(s, mathLifts, mathLifts[0].path)
	if err != nil {
		return "", err
	}
	parts = append(parts, "public static class RocketMath\n{\n"+strings.Join(math, "\n\n")+"\n}")

	extensions, err := liftMembers(s, extensionLifts, extensionLifts[0].path)
	if err != nil {
		return "", err
	}
	parts = append(parts, "public static class ExtensionMethods\n{\n"+strings.Join(extensions, "\n\n")+"\n}")

	chemistry, err := liftMembers(s, chemistryLifts, chemistryPath)
	if err != nil {
		return "", err
	}
	parts = append(parts, "public static class Chemistry\n{\n"+strings.Join(chemistry, "\n\n")+"\n}")

	logicable, err := liftMembers(s, logicableLifts, logicablePath)
	if err != nil {
		return "", err
	}
	parts = append(parts, "public static class Logicable\n{\n"+strings.Join(logicable, "\n\n")+"\n}")

	gameManager, err := sliceGameManager(s)
	if err != nil {
		return "", err
	}
	parts = append(parts, gameManager)

	housing, err := sliceHousing(s)
	if err != nil {
		return "", err
	}
	parts = append(parts, housing)

	if err := recordCited(s); err != nil {
		return "", err
	}

	parts = append(parts, contractShims, animatorShim, mathfShim, randomShim)
	return supportHeader + strings.Join(parts, "\n\n") + "\n", nil
}

const supportHeader = `using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
using System.Text.RegularExpressions;

`

// contractShims are the interfaces the chip dispatches through, the scene
// objects device bodies read state off, and the static hooks operation
// bodies reach. NetworkManager's four error properties differ by server vs.
// client but never change the stored value a client run sees.
const contractShims = `// Narrowed from IScriptEnum.cs to the two entry points a variable parser is
// driven through. Dropped: Parse, Count, TryParse, IsHashType, IsDeprecated
// (editor completion) and MakePage (builds a help page from scene objects).
public interface IScriptEnum
{
	void Execute(ref bool isValueSet, ref double value, string code, InstructionInclude propertiesToUse);

	void Execute(ref bool isValueSet, ref int value, string code, InstructionInclude propertiesToUse);
}

// Narrowed from ILogicable.cs. Dropped: GetAsThing/ToTooltip (scene objects)
// and GetNextSlotId/IsLogic*able (no operation reaches them). The base list is
// the game's: the batch sort orders by a name declared on IReferencable.
public interface ILogicable : IReferencable
{
	int TotalSlots { get; }

	bool HasAnySlots { get; }

	Slot GetSlot(int slotIndex);

	int GetPrefabHash();

	int GetNameHash();

	bool CanLogicRead(LogicType logicType);

	bool CanLogicWrite(LogicType logicType);

	void SetLogicValue(LogicType logicType, double value);

	double GetLogicValue(LogicType logicType);

	bool CanLogicRead(LogicSlotType logicSlotType, int slotId);

	double GetLogicValue(LogicSlotType logicSlotType, int slotId);
}

// Narrowed from IReferencable.cs to the id a slot read answers and the name the
// batch sort orders by. Dropped: the network dirty flag, the destruction flag
// and the two registry hooks, none of which a logic read reaches.
public interface IReferencable
{
	long ReferenceId { get; set; }

	string DisplayName { get; }
}

// Narrowed from IQuantity.cs and the ITradable it extends, to the two
// readings the Quantity and MaxQuantity slot arms take.
public interface IQuantity
{
	float GetQuantity { get; }

	float GetMaxQuantity { get; }
}

// Narrowed from IndestructableDamageState.cs to the one ratio the Damage slot
// arm reads. The game computes it from nine damage kinds over a prefab
// maximum; the ratio is set directly here, still the float the game holds it as.
public class IndestructableDamageState
{
	public float TotalRatio;
}

// Narrowed from Assets/Scripts/Objects/Pipes/ISlotWriteable.cs.
public interface ISlotWriteable : ILogicable
{
	bool CanLogicWrite(LogicSlotType logicType, int slotId);

	void SetLogicValue(LogicSlotType logicType, int slotId, double value);
}

// Narrowed from IConnected.cs. The game answers a CableNetwork, which is
// itself an ILogicable; this unit does not lift that class.
public interface IConnected
{
	ILogicable GetNetwork(int networkIndex);
}

// Narrowed from CableNetwork.cs to the device list a pin lookup filters
// against and a batch form sorts. The game's rebuilds itself from the cable
// graph when dirty; here it is set directly and stays put.
public class CableNetwork
{
	public List<Device> DataDeviceList = new List<Device>();
}

// Narrowed from Cable.cs to the network a device's power cable is on, which
// is what GetUsedPower compares against.
public class Cable
{
	public CableNetwork CableNetwork;
}

// Narrowed from ColorSwatch.cs to the one field IsLogicSelectableColor reads.
public class ColorSwatch
{
	public bool PaintOnly;
}

// Narrowed from Atmosphere.cs to the five readings the two logic value paths
// take off it. The game's three quantities are fixed-point structs; here they
// are the doubles ToDouble answers, set directly and held still.
public class Atmosphere
{
	public bool Sparked;

	public double TotalMoles, PressureGassesAndLiquids, Temperature, Volume;
}

// Not lifted: Interactable.cs. The game's setter also notifies a parent,
// queues a sound and marks the object dirty; none of that changes the
// stored number, which is the whole of what a logic read can see.
// JoinInProgressSync is a serialized prefab flag every state arm reads through.
public class Interactable
{
	public bool JoinInProgressSync = true;

	private int _state;

	public int State
	{
		get
		{
			if (JoinInProgressSync)
			{
				return _state;
			}
			return 0;
		}
		set
		{
			_state = value;
		}
	}
}

// Not lifted: OnServer.cs. The game's body also refuses while paused, queues
// off-thread interactions, skips inactive objects and drives an animation;
// what a chip can observe of all that is the interactable's resulting state.
public static class OnServer
{
	public static void Interact(Interactable interactable, int state, bool skipAnimation = false)
	{
		if (interactable == null)
		{
			return;
		}
		interactable.State = state;
	}
}

// Narrowed from ICircuitHolder.cs. Dropped: HaltAndCatchFire (returns a
// UniTask) and the editor's source-code and logic binding surface.
public interface ICircuitHolder
{
	void ClearError();

	void RaiseError(int state);

	ILogicable GetLogicableFromIndex(int deviceIndex, int networkIndex = int.MinValue);

	ILogicable GetLogicableFromId(int deviceId, int networkIndex = int.MinValue);

	List<ILogicable> GetBatchOutput();

	bool IsValidIndex(int index);

	void SetDeviceLabel(int index, string label);

	void HasPut();
}

// Not lifted: the LogicLightComponent held as _MemoryLight. Flashing it is
// the only stack-instruction effect besides moving a value and is not
// observable from a program; here it is never null, unlike the game's field.
public class MemoryLight
{
	public int Flashes;

	public int Resets;

	public void Flash(LogicMemoryState state)
	{
		Flashes++;
	}

	public void Reset()
	{
		Resets++;
	}
}

public static class NetworkManager
{
	public static bool IsServer;

	public static bool IsClient;
}`

// animatorShim replaces Animator.StringToHash, the hash every device, name
// and reagent is addressed by: CRC32-IEEE truncated to int32, the same
// algorithm tools/isagen hashes the device roster with.
const animatorShim = `public static class Animator
{
	private static readonly uint[] Table = BuildTable();

	private static uint[] BuildTable()
	{
		uint[] table = new uint[256];
		for (uint i = 0; i < 256; i++)
		{
			uint c = i;
			for (int bit = 0; bit < 8; bit++)
			{
				c = ((c & 1) != 0) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
			}
			table[i] = c;
		}
		return table;
	}

	public static int StringToHash(string text)
	{
		if (text == null)
		{
			return 0;
		}
		uint crc = 0xFFFFFFFFu;
		foreach (char c in text)
		{
			crc = Table[(crc ^ (byte)c) & 0xFF] ^ (crc >> 8);
		}
		return unchecked((int)(crc ^ 0xFFFFFFFFu));
	}
}`

// mathfShim replaces Mathf.Clamp, which narrows every one-bit device write
// before it is cast to an integer. Not lifted since UnityEngine is not in the
// decompile; its bounds check with < and >, both false for NaN, so a NaN
// passes through untouched to the cast — unlike ExtensionMethods.Clamp, which sorts it low.
const mathfShim = `public static class Mathf
{
	public static float Clamp(float value, float min, float max)
	{
		if (value < min)
		{
			value = min;
		}
		else if (value > max)
		{
			value = max;
		}
		return value;
	}
}`

// randomShim replaces the generator _RAND_Operation draws from. The game
// holds an unseeded static Random, so its sequence is fixed once per process
// and two runs in one process draw differently. This is System.Random under
// whichever runtime compiles the unit — not the game's sequence, but a function of the seed.
const randomShim = `public static class HarnessRandom
{
	// A run that never sets a seed still gets a reproducible sequence rather
	// than a fresh one, so an accidental difference between two runs is not
	// mistaken for the program having changed.
	public const int DefaultSeed = 0x1c11c;

	private static Random _source = new Random(DefaultSeed);

	public static void Reseed(int seed)
	{
		_source = new Random(seed);
	}

	public static double NextDouble()
	{
		return _source.NextDouble();
	}
}`
