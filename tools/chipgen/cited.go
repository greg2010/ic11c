package main

import "fmt"

// citedShim is a game construct a hand-written stand-in answers for: a
// field, constant, or narrowed interface standing in for text the emitted
// unit does not carry. Reading it here fingerprints it, so a game update
// that rewrites it stops the slice rather than going unnoticed.
type citedShim struct {
	path string
	name string
	// whole records the type's entire text. It is for a declaration with no
	// bodies in it, where every line is part of what a narrowing left out.
	whole bool
	// members are the declarations the stand-in answers for, named one at a
	// time where recording the whole type would trip on text no stand-in reads.
	members []string
	// note says what the stand-in claims, where the shim's own comment in the
	// emitted unit does not already spell it out.
	note string
}

// citedShims is every game construct a hand-written stand-in in this
// slicer answers for. Two exceptions cannot be here: Mathf.Clamp and
// Animator.StringToHash, written rather than lifted because UnityEngine is
// not in the decompile.
var citedShims = []citedShim{
	{path: "Assets/Scripts/Objects/Pipes/ILogicable.cs", name: "ILogicable", whole: true},
	{path: "IReferencable.cs", name: "IReferencable", whole: true},
	{path: "Assets/Scripts/Objects/Pipes/ISlotWriteable.cs", name: "ISlotWriteable", whole: true},
	{path: "Assets/Scripts/Objects/Pipes/IConnected.cs", name: "IConnected", whole: true,
		note: "the shim answers an ILogicable where the game answers the CableNetwork that is one"},
	{path: "Assets/Scripts/Objects/Electrical/ICircuitHolder.cs", name: "ICircuitHolder", whole: true},
	{path: "Assets/Scripts/Objects/Electrical/IScriptEnum.cs", name: "IScriptEnum", whole: true},
	{path: "Assets/Scripts/Objects/Items/IQuantity.cs", name: "IQuantity", whole: true},
	{path: "Trading/ITradable.cs", name: "ITradable", whole: true,
		note: "where GetQuantity is declared; the IQuantity shim narrows both interfaces into one"},

	{path: "Assets/Scripts/Objects/Interactable.cs", name: "Interactable",
		members: []string{"public int State", "public bool JoinInProgressSync", "public bool IsValidToSend()"}},
	{path: "OnServer.cs", name: "OnServer",
		members: []string{"public static void Interact(Interactable interactable, int state, bool skipAnimation = false)"}},
	{path: "Assets/Scripts/Networks/CableNetwork.cs", name: "CableNetwork",
		members: []string{"public List<Device> DataDeviceList"}},
	{path: "Assets/Scripts/Objects/Electrical/Cable.cs", name: "Cable",
		members: []string{"public CableNetwork CableNetwork"}},
	{path: "Assets/Scripts/Objects/ColorSwatch.cs", name: "ColorSwatch",
		members: []string{"public bool PaintOnly"}},
	{path: "Assets/Scripts/Atmospherics/Atmosphere.cs", name: "Atmosphere",
		members: []string{"public bool Sparked", "public MoleQuantity TotalMoles",
			"public PressurekPa PressureGassesAndLiquids", "public TemperatureKelvin Temperature",
			"public VolumeLitres Volume"},
		note: "the shim holds the doubles those quantities answer, and a run sets them rather than simulating"},
	{path: "Assets/Scripts/Objects/IndestructableDamageState.cs", name: "IndestructableDamageState",
		members: []string{"public virtual float TotalRatio"}},
	{path: "Assets/Scripts/Objects/Items/Stackable.cs", name: "Stackable",
		members: []string{"public float GetMaxQuantity", "public override float GetQuantity"}},
	{path: "Assets/Scripts/Objects/Items/GasFilter.cs", name: "GasFilter",
		members: []string{"public Chemistry.GasType FilterType"}},
	{path: "Assets/Scripts/Objects/Electrical/LogicLightComponent.cs", name: "LogicLightComponent",
		members: []string{"public void Flash(LogicMemoryState state)", "public void Reset()"},
		note:    "the housing's memory light; the shim counts the calls and answers nothing back"},
	{path: "Assets/Scripts/Networking/NetworkManager.cs", name: "NetworkManager",
		members: []string{"public static bool IsClient", "public static bool IsServer"},
		note:    "the shim stores both where the game derives them from the session's role"},

	// The property the housing's batch edit replaces, and the one place the
	// game clears its cache. Together they are what makes sorting on every call
	// the same answer as the game's cached one.
	{path: logicUnitBasePath, name: "LogicUnitBase",
		members: []string{"public List<ILogicable> InputNetwork1DevicesSorted", "public virtual void OnNetworkChange()"}},

	// The reference-id lookup the housing shim answers out of a dictionary a run
	// fills, where the game walks the world's object registry and filters
	// against the housing's own data network.
	{path: housingPath, name: "CircuitHousing",
		members: []string{"public ILogicable GetLogicableFromId(int deviceId, int networkIndex = int.MinValue)"}},

	// The name the batch sort orders by, which the device shim carries as a
	// field, and the hash the game keeps in step with it. Slots is the list a
	// prefab builds and a run fills.
	{path: thingPath, name: "Thing",
		members: []string{"public virtual string DisplayName", "public virtual string CustomName",
			"public int GetNameHash()", "public int GetPrefabHash()",
			"public virtual double GasRatio(LogicType logicType)", "public List<Slot> Slots"}},

	// The network lookup a device code with a suffix resolves through. The shim
	// reads a dictionary a run fills and answers an ILogicable, where the game
	// walks the device's open ends and answers the CableNetwork that is one.
	{path: devicePath, name: "Device", members: []string{"public CableNetwork GetNetwork(int networkIndex)"}},

	{path: slotPath, name: "Slot", members: []string{"public DynamicThing Occupant"}},
	{path: batteryPath, name: "BatteryCell", members: []string{"public float PowerStored"}},
}

// recordCited reads every construct a stand-in answers for, which fingerprints
// it. Nothing is emitted; see [citedShim].
func recordCited(s *slicing) error {
	for _, want := range citedShims {
		src, err := s.read(want.path)
		if err != nil {
			return fmt.Errorf("stand-in for %s: %w", want.name, err)
		}
		typeDecl, err := src.topLevelType(want.name)
		if err != nil {
			return fmt.Errorf("stand-in for %s: %w", want.name, err)
		}
		if want.whole {
			if err := src.top().cutTree(typeDecl); err != nil {
				return fmt.Errorf("stand-in for %s: %w", want.name, err)
			}
			continue
		}
		body := src.top().scopeOf(typeDecl)
		for _, member := range want.members {
			if _, err := body.member(member); err != nil {
				return fmt.Errorf("stand-in for %s: %s: %s.%w", want.name, src.rel, want.name, err)
			}
		}
	}
	return nil
}
