using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

// Harness is a line-oriented subprocess driving one program at a time against a
// simulated housing. Every command answers on one line beginning "ok" or "err",
// except "state" and "fixture trace", which answer a block terminated by "end".
public static class Harness
{
	private const string Prompt = "ok";

	private static ProgrammableChip _chip;
	private static CircuitHousing _housing;
	private static readonly Dictionary<string, Device> _devices = new Dictionary<string, Device>();

	// Writes fixture devices have recorded, in program order. Empty unless the
	// process was started with FixturesFlag.
	private static readonly List<FixtureWrite> _writes = new List<FixtureWrite>();

	// FixturesFlag decides once, from argv, whether this process builds permissive
	// devices; no command can change it afterward. A driver asking what the game's
	// own devices do must run with no arguments, so its devices are always the game's own.
	private const string FixturesFlag = "--fixtures";

	private static bool _fixtures;

	public static int Main(string[] args)
	{
		// An unknown flag stops the process rather than being ignored, so a
		// misspelled flag cannot silently produce the wrong kind of harness.
		foreach (string arg in args)
		{
			if (arg != FixturesFlag)
			{
				Console.Error.WriteLine("harness: unknown argument " + arg + "; the only one is " + FixturesFlag);
				return 2;
			}
			_fixtures = true;
		}

		Reagent.GenerateReagentTypeLookup();
		Reset();

		Console.OutputEncoding = new UTF8Encoding(false);
		string line;
		while ((line = Console.In.ReadLine()) != null)
		{
			if (line.Length == 0)
			{
				continue;
			}
			try
			{
				if (!Dispatch(line))
				{
					break;
				}
			}
			catch (Exception e)
			{
				// A malformed command must not take the process down: the caller is
				// driving many programs through one process and needs the failure
				// attributed to this command.
				Console.WriteLine("err " + e.GetType().Name + ": " + Flatten(e.Message));
			}
			Console.Out.Flush();
		}
		return 0;
	}

	private static bool Dispatch(string line)
	{
		string[] argv = line.Split(' ');
		switch (argv[0])
		{
		case "quit":
			Arity(argv, "quit");
			return false;
		case "reset":
			Arity(argv, "reset");
			Reset();
			Console.WriteLine(Prompt);
			return true;
		case "src":
			// Base64 so a program's own newlines and spacing survive this
			// line-oriented protocol.
			Arity(argv, "src <program>");
			_chip.SetSourceCode(Decode(argv[1]));
			Console.WriteLine(Prompt);
			return true;
		case "reg":
			Arity(argv, "reg <index> <bits>");
			_chip.HarnessRegisters[int.Parse(argv[1], CultureInfo.InvariantCulture)] = ParseBits(argv[2]);
			Console.WriteLine(Prompt);
			return true;
		case "stack":
			Arity(argv, "stack <address> <bits>");
			_chip.HarnessStack[int.Parse(argv[1], CultureInfo.InvariantCulture)] = ParseBits(argv[2]);
			Console.WriteLine(Prompt);
			return true;
		case "clock":
			Clock(argv);
			Console.WriteLine(Prompt);
			return true;
		case "seed":
			// seed <n> arms the sequence rand draws from and restarts it. The game's
			// generator is an unseeded process-global System.Random, so no seed
			// reproduces the game's own sequence; what a seed buys is that two runs
			// of one program draw the same numbers.
			Arity(argv, "seed <n>");
			_seed = int.Parse(argv[1], CultureInfo.InvariantCulture);
			HarnessRandom.Reseed(_seed);
			Console.WriteLine(Prompt);
			return true;
		case "colors":
			// colors <count> [<paintOnlyIndex>...] arms the palette a Color write is
			// clamped into; a run with no palette refuses every Color write.
			Colors(argv);
			Console.WriteLine(Prompt);
			return true;
		case "run":
			// run <count> retires up to count instructions and answers 1 if the tick
			// ended by exhausting them with the program still mid-tick, 0 otherwise —
			// the chip does not distinguish that from a yield, so this bit is the
			// only place the two are told apart.
			Arity(argv, "run <count>");
			_chip.Execute(int.Parse(argv[1], CultureInfo.InvariantCulture));
			Console.WriteLine(Prompt + " " + (_chip.HarnessBudgetExhausted ? "1" : "0"));
			return true;
		case "runto":
			// runto <count> <ticks> ticks the chip to a stop, at most ticks ticks of
			// count instructions each, and answers how the last tick ended and how
			// many ran. A caller that must act between ticks uses run instead.
			RunTo(argv);
			return true;
		case "limits":
			// limits answers the game's own RUN_COUNT and pin count, so a driver
			// never holds a second copy of those constants to drift out of step with
			// the lifted class.
			Arity(argv, "limits");
			Console.WriteLine(Prompt +
				" " + CircuitHousing.RUN_COUNT.ToString(CultureInfo.InvariantCulture) +
				" " + _housing.Devices.Length.ToString(CultureInfo.InvariantCulture));
			return true;
		case "ip":
			SetAddress(argv);
			Console.WriteLine(Prompt);
			return true;
		case "dev":
			Device(argv);
			Console.WriteLine(Prompt);
			return true;
		case "get":
			Arity(argv, "get <target> <logicType>");
			Console.WriteLine(Prompt + " " + Bits(Resolve(argv[1]).GetLogicValue(ParseEnum<LogicType>(argv[2]))));
			return true;
		case "gets":
			Arity(argv, "gets <target> <slotIndex> <logicSlotType>");
			Console.WriteLine(Prompt + " " + Bits(Resolve(argv[1]).GetLogicValue(
				ParseEnum<LogicSlotType>(argv[3]), int.Parse(argv[2], CultureInfo.InvariantCulture))));
			return true;
		case "fixture":
			return Fixture(argv);
		case "state":
			Arity(argv, "state");
			WriteState();
			return true;
		default:
			Console.WriteLine("err unknown command " + argv[0]);
			return true;
		}
	}

	private static float _clockValue;

	private static float _clockStep;

	private static int _seed = HarnessRandom.DefaultSeed;

	private static void Reset()
	{
		_housing = new CircuitHousing();
		_devices.Clear();
		_writes.Clear();
		GameManager.CustomColors.Clear();
		GameManager.SetClock(_clockValue, _clockStep);
		HarnessRandom.Reseed(_seed);
		_chip = new ProgrammableChip();
		_chip.CircuitHousing = _housing;
		_housing.ProgrammableChip = _chip;
		_chip.SetSourceCode(string.Empty);
	}

	// clock <bits> pins the clock; clock <bits> <bits> also sets how far each
	// reading advances it (the sleep clock is a float; this is the one place the
	// protocol narrows). No step means a step of zero, which is a clock two
	// readings never distinguish — so a sleep that never expires.
	private static void Clock(string[] argv)
	{
		if (argv.Length < 2 || argv.Length > 3)
		{
			throw new ArgumentException("expected \"clock <bits>\" or \"clock <bits> <step>\", got " +
				argv.Length.ToString(CultureInfo.InvariantCulture) + " word(s)");
		}
		_clockValue = (float)ParseBits(argv[1]);
		_clockStep = (argv.Length == 3) ? (float)ParseBits(argv[2]) : 0f;
		GameManager.SetClock(_clockValue, _clockStep);
	}

	// The five ways Execute can end. Three appear in the state block (compile
	// error, fault, program counter outside the program); a yield and a spent
	// budget do not, because the chip leaves the same address, error state, and
	// line count for both.
	private const string StopEnded = "ended";

	private const string StopFaulted = "faulted";

	private const string StopCompileError = "compile_error";

	private const string StopSuspended = "suspended";

	private const string StopBudget = "budget";

	// RunTo drives the tick loop the game drives. A yield and a spent budget both
	// continue the loop — mirroring the game — while the reply still names which
	// one ended the last tick. A count or ticks below 1 is refused: Execute would
	// retire nothing and the loop would spend the whole limit without moving the chip.
	private static void RunTo(string[] argv)
	{
		Arity(argv, "runto <count> <ticks>");
		int count = int.Parse(argv[1], CultureInfo.InvariantCulture);
		int ticks = int.Parse(argv[2], CultureInfo.InvariantCulture);
		if (count < 1 || ticks < 1)
		{
			throw new ArgumentException("runto takes an instruction count and a tick limit of at least 1, got " +
				count.ToString(CultureInfo.InvariantCulture) + " and " +
				ticks.ToString(CultureInfo.InvariantCulture));
		}
		int ran = 0;
		string reason;
		do
		{
			_chip.Execute(count);
			ran++;
			reason = WhyStopped();
		}
		while ((reason == StopSuspended || reason == StopBudget) && ran < ticks);
		Console.WriteLine(Prompt + " " + reason + " " + ran.ToString(CultureInfo.InvariantCulture));
	}

	// The order matters: a program that failed to compile still ran its compiled
	// prefix, so a compile error takes priority over a fault; and a fault leaves
	// the address on the faulting line, which would otherwise read as still running.
	private static string WhyStopped()
	{
		if (_chip.HarnessCompileErrorType != ProgrammableChipException.ICExceptionType.None)
		{
			return StopCompileError;
		}
		if (_chip.HarnessErrorType != ProgrammableChipException.ICExceptionType.None)
		{
			return StopFaulted;
		}
		if (_chip.HarnessNextAddress < 0 || _chip.HarnessNextAddress >= _chip.HarnessLineCount)
		{
			return StopEnded;
		}
		return _chip.HarnessBudgetExhausted ? StopBudget : StopSuspended;
	}

	// ip <line> moves the program counter without running anything, the only way
	// to reach an instruction control flow did not arrive at. A line outside the
	// program is refused rather than clamped, unlike the chip's own LineNumber
	// setter, so an out-of-range request cannot silently land on the last instruction.
	private static void SetAddress(string[] argv)
	{
		Arity(argv, "ip <line>");
		int line = int.Parse(argv[1], CultureInfo.InvariantCulture);
		if (line < 0 || line >= _chip.HarnessLineCount)
		{
			throw new ArgumentException("line " + line.ToString(CultureInfo.InvariantCulture) +
				" is outside the " + _chip.HarnessLineCount.ToString(CultureInfo.InvariantCulture) +
				" line program");
		}
		_chip.LineNumber = line;
	}

	// Arity refuses a command whose word count does not match form, rather than
	// let a short command index out of range or a long one silently drop extra words.
	private static void Arity(string[] argv, string form)
	{
		if (argv.Length != form.Split(' ').Length)
		{
			throw new ArgumentException("expected \"" + form + "\", got " +
				argv.Length.ToString(CultureInfo.InvariantCulture) + " word(s)");
		}
	}

	private static void Colors(string[] argv)
	{
		if (argv.Length < 2)
		{
			throw new ArgumentException("expected \"colors <count> [<paintOnlyIndex>...]\", got " +
				argv.Length.ToString(CultureInfo.InvariantCulture) + " word(s)");
		}
		GameManager.CustomColors.Clear();
		int count = int.Parse(argv[1], CultureInfo.InvariantCulture);
		for (int i = 0; i < count; i++)
		{
			GameManager.CustomColors.Add(new ColorSwatch());
		}
		for (int i = 2; i < argv.Length; i++)
		{
			GameManager.CustomColors[int.Parse(argv[i], CultureInfo.InvariantCulture)].PaintOnly = true;
		}
	}

	private static void Device(string[] argv)
	{
		if (argv.Length < 3)
		{
			throw new ArgumentException("dev takes a target and a verb");
		}
		string spec = argv[1];
		if (argv[2] == "new")
		{
			// A target is a pin index, "nN" for the logicable answering network
			// suffix N, or "db" for the housing itself, which is reset rather than created.
			if (argv.Length > 4)
			{
				throw new ArgumentException("expected \"dev <target> new [kind]\", got " +
					argv.Length.ToString(CultureInfo.InvariantCulture) + " word(s)");
			}
			if (spec == "db")
			{
				throw new ArgumentException("the housing is not created, it is reset");
			}
			Device fresh = NewThing(argv.Length > 3 ? argv[3] : "device");
			if (spec[0] == 'n')
			{
				int network = int.Parse(spec.Substring(1), CultureInfo.InvariantCulture);
				Occupied(_housing.Networks.ContainsKey(network), spec);
				_housing.Networks[network] = fresh;
			}
			else
			{
				int pin = int.Parse(spec, CultureInfo.InvariantCulture);
				if (pin < 0 || pin >= _housing.Devices.Length)
				{
					throw new ArgumentException("pin " + spec + " is outside the housing's " +
						_housing.Devices.Length.ToString(CultureInfo.InvariantCulture) + " pins");
				}
				Occupied(_housing.Devices[pin] != null, spec);
				_housing.Devices[pin] = fresh;
			}
			_devices[spec] = fresh;
			return;
		}
		Device target = Resolve(spec);
		switch (argv[2])
		{
		case "flag":
			Arity(argv, "dev <target> flag <name> <0|1>");
			SetFlag(target, argv[3], argv[4] != "0");
			break;
		case "hash":
			// Hash and name hash are set independently here, unlike the game's own
			// setter (which recomputes name hash from name), so a run can pair
			// values the game never would. "name" below sets both together.
			Arity(argv, "dev <target> hash <prefabHash> <nameHash>");
			target.PrefabHash = int.Parse(argv[3], CultureInfo.InvariantCulture);
			target.NameHash = int.Parse(argv[4], CultureInfo.InvariantCulture);
			break;
		case "name":
			// Base64 because a display name may carry spaces and the protocol is
			// one command per line split on them. Name hash follows name, as the
			// game's setter does.
			Arity(argv, "dev <target> name <base64>");
			target.DisplayName = Decode(argv[3]);
			target.NameHash = Animator.StringToHash(target.DisplayName);
			break;
		case "sync":
			// The game gates every device-state read through an interactable that
			// answers zero unless the session synchronises it; clearing
			// JoinInProgressSync here is how a run asks what an unsynced state reads.
			Arity(argv, "dev <target> sync <logicType> <0|1>");
			Interactable(target, ParseEnum<LogicType>(argv[3])).JoinInProgressSync = argv[4] != "0";
			break;
		case "network":
			Arity(argv, "dev db network <0|1>");
			Housing(target).InputNetwork1 = (argv[3] == "0") ? null : new CableNetwork();
			break;
		case "set":
			Arity(argv, "dev <target> set <logicType> <bits>");
			SetValue(target, ParseEnum<LogicType>(argv[3]), ParseBits(argv[4]));
			break;
		case "ratio":
			Arity(argv, "dev <target> ratio <logicType> <bits>");
			target.GasRatios[ParseEnum<LogicType>(argv[3])] = ParseBits(argv[4]);
			break;
		case "volume":
			Arity(argv, "dev <target> volume <bits>");
			Atmos(target).Volume = ParseBits(argv[3]);
			break;
		case "modes":
			Arity(argv, "dev <target> modes <count>");
			target.Modes = new string[int.Parse(argv[3], CultureInfo.InvariantCulture)];
			break;
		case "cable":
			Arity(argv, "dev <target> cable <0|1>");
			target.PowerCable = (argv[3] == "0") ? null : new Cable();
			break;
		case "slot":
			Arity(argv, "dev <target> slot <index> <slotClass>");
			int index = int.Parse(argv[3], CultureInfo.InvariantCulture);
			while (target.Slots.Count <= index)
			{
				target.Slots.Add(new Slot());
			}
			target.Slots[index].Type = ParseEnum<Slot.Class>(argv[4]);
			target.HasAnySlots = true;
			break;
		case "occupant":
			Arity(argv, "dev <target> occupant <index> <target>");
			target.Slots[int.Parse(argv[3], CultureInfo.InvariantCulture)].Occupant = Resolve(argv[4]);
			break;
		case "slottype":
			Arity(argv, "dev <target> slottype <slotClass>");
			target.SlotType = ParseEnum<Slot.Class>(argv[3]);
			break;
		case "sorting":
			Arity(argv, "dev <target> sorting <sortingClass>");
			target.SortingClass = ParseEnum<SortingClass>(argv[3]);
			break;
		case "damage":
			Arity(argv, "dev <target> damage <bits>");
			target.DamageState.TotalRatio = (float)ParseBits(argv[3]);
			break;
		case "quantity":
			Arity(argv, "dev <target> quantity <bits> <bits>");
			Kind<Stackable>(target).GetQuantity = (float)ParseBits(argv[3]);
			Kind<Stackable>(target).GetMaxQuantity = (float)ParseBits(argv[4]);
			break;
		case "filter":
			Arity(argv, "dev <target> filter <gasType>");
			Kind<GasFilter>(target).FilterType = ParseEnum<Chemistry.GasType>(argv[3]);
			break;
		case "charge":
			Arity(argv, "dev <target> charge <bits> <bits>");
			Kind<BatteryCell>(target).PowerStored = (float)ParseBits(argv[3]);
			Kind<BatteryCell>(target).PowerMaximum = (float)ParseBits(argv[4]);
			break;
		case "mixture":
			Arity(argv, "dev <target> mixture <reagent> <bits>");
			Seeded(target.ReadableReagentMixture.HarnessSet(FindReagent(argv[3]), ParseBits(argv[4])), argv[3]);
			break;
		case "required":
			Arity(argv, "dev <target> required <reagent> <bits>");
			Seeded(Kind<ReagentUser>(target).RequiredReagents.HarnessSet(FindReagent(argv[3]), ParseBits(argv[4])), argv[3]);
			break;
		case "recipe":
			Arity(argv, "dev <target> recipe <reagent> <bits>");
			Seeded(Kind<ReagentUser>(target).CurrentRecipe.HarnessSet(FindReagent(argv[3]), ParseBits(argv[4])), argv[3]);
			break;
		case "reagentprefab":
			Arity(argv, "dev <target> reagentprefab <reagentHash> <prefabHash>");
			Kind<ReagentUser>(target).ReagentPrefabs[int.Parse(argv[3], CultureInfo.InvariantCulture)] =
				int.Parse(argv[4], CultureInfo.InvariantCulture);
			break;
		case "batch":
			Arity(argv, "dev <target> batch");
			DataNetwork().DataDeviceList.Add(target);
			break;
		case "id":
			Arity(argv, "dev <target> id <deviceId>");
			target.ReferenceId = long.Parse(argv[3], CultureInfo.InvariantCulture);
			_housing.ById[int.Parse(argv[3], CultureInfo.InvariantCulture)] = target;
			break;
		default:
			throw new ArgumentException("unknown dev verb " + argv[2]);
		}
	}

	// A name nothing matches is refused rather than seeded onto nothing: the
	// game's Find answers null for one, and every table read then answers 0.0 —
	// indistinguishable from a seed of zero that landed.
	private static Reagent FindReagent(string name)
	{
		Reagent found = Reagent.Find(name);
		if (found == null)
		{
			throw new ArgumentException("no reagent named " + name);
		}
		return found;
	}

	// Fires only when the value table was generated from a shorter reading of the
	// reagent roster than Reagent.Find uses; Get would otherwise answer 0.0 for
	// the seed and it would read back as a seed of zero.
	private static void Seeded(bool seeded, string reagent)
	{
		if (!seeded)
		{
			throw new ArgumentException("the table has no member for reagent " + reagent +
				"; it was generated from a different roster than Reagent.Find reads");
		}
	}

	// Replacing a device in place would strand the displaced one in lists keyed
	// to nothing this cleans up — the batch list, the reference-id index — so a
	// second device on one target is refused rather than swapped.
	private static void Occupied(bool taken, string spec)
	{
		if (taken)
		{
			throw new ArgumentException("target " + spec + " already holds a device; reset to build a fresh housing");
		}
	}

	private static Device NewThing(string kind)
	{
		switch (kind)
		{
		case "device": return new Device();
		case "stackable": return new Stackable();
		case "gasfilter": return new GasFilter();
		case "battery": return new BatteryCell();
		case "reagentuser": return new ReagentUser();
		default: throw new ArgumentException("no thing kind named " + kind);
		}
	}

	// The whole permissive surface is behind this verb, and it exists only in a
	// process started with FixturesFlag; see FixtureDevice.
	private static bool Fixture(string[] argv)
	{
		if (!_fixtures)
		{
			throw new ArgumentException("this harness was started without " + FixturesFlag +
				", and a device that answers every property must not reach a run that asks what the game's own devices refuse");
		}
		if (argv.Length < 2)
		{
			throw new ArgumentException("fixture takes a verb: new, set, slot or trace");
		}
		switch (argv[1])
		{
		case "new":
			Arity(argv, "fixture new <pin>");
			NewFixture(argv[2]);
			break;
		case "set":
			Arity(argv, "fixture set <pin> <logicType> <bits>");
			AsFixture(argv[2]).Seed(ParseEnum<LogicType>(argv[3]), ParseBits(argv[4]));
			break;
		case "slot":
			Arity(argv, "fixture slot <pin> <slotIndex> <logicSlotType> <bits>");
			AsFixture(argv[2]).SeedSlot(ParseEnum<LogicSlotType>(argv[4]),
				int.Parse(argv[3], CultureInfo.InvariantCulture), ParseBits(argv[5]));
			break;
		case "trace":
			Arity(argv, "fixture trace");
			WriteTrace();
			return true;
		default:
			throw new ArgumentException("unknown fixture verb " + argv[1]);
		}
		Console.WriteLine(Prompt);
		return true;
	}

	// Builds the arrangement a real housing has: the device sits on a pin, on the
	// data cable the batch instructions aggregate over (created here if the
	// housing has none, unlike the faithful "network" verb), and answers its
	// reference id. A pin already holding a device is refused, for the same reason as Occupied.
	private static void NewFixture(string text)
	{
		int pin = int.Parse(text, CultureInfo.InvariantCulture);
		// Filed under the pin's own spelling, so a later command reaches it
		// whichever spelling the caller used first.
		string spec = pin.ToString(CultureInfo.InvariantCulture);
		if (pin < 0 || pin >= _housing.Devices.Length)
		{
			throw new ArgumentException("pin " + spec + " is outside the housing's " +
				_housing.Devices.Length.ToString(CultureInfo.InvariantCulture) + " pins");
		}
		Occupied(_housing.Devices[pin] != null, spec);
		if (_housing.InputNetwork1 == null)
		{
			_housing.InputNetwork1 = new CableNetwork();
		}
		FixtureDevice fresh = new FixtureDevice(pin, _writes);
		fresh.ReferenceId = pin + 1;
		fresh.DisplayName = "d" + spec;
		_devices[spec] = fresh;
		_housing.Devices[pin] = fresh;
		_housing.InputNetwork1.DataDeviceList.Add(fresh);
		_housing.ById[pin + 1] = fresh;
	}

	private static FixtureDevice AsFixture(string spec)
	{
		return Kind<FixtureDevice>(Resolve(spec));
	}

	// Every write a fixture device recorded, in program order, terminated like
	// the state block. Only writes that changed something appear, because the
	// chip skips a store that would not change the current value. A property is
	// written as its ordinal since a program can write one the enum does not name.
	private static void WriteTrace()
	{
		StringBuilder text = new StringBuilder();
		foreach (FixtureWrite write in _writes)
		{
			if (write.Slot == FixtureDevice.NoSlot)
			{
				text.Append("w ").Append(write.Pin.ToString(CultureInfo.InvariantCulture))
					.Append(' ').Append(write.Property.ToString(CultureInfo.InvariantCulture))
					.Append(' ').Append(Bits(write.Value)).Append('\n');
				continue;
			}
			text.Append("ws ").Append(write.Pin.ToString(CultureInfo.InvariantCulture))
				.Append(' ').Append(write.Slot.ToString(CultureInfo.InvariantCulture))
				.Append(' ').Append(write.Property.ToString(CultureInfo.InvariantCulture))
				.Append(' ').Append(Bits(write.Value)).Append('\n');
		}
		text.Append("end");
		Console.WriteLine(text.ToString());
	}

	// Narrows to the class a verb needs, so a mismatched target is a named
	// refusal rather than a value nothing reads.
	private static T Kind<T>(Device device) where T : Device
	{
		T narrowed = device as T;
		if (narrowed == null)
		{
			throw new ArgumentException("only a " + typeof(T).Name + " carries that, not a " + device.GetType().Name);
		}
		return narrowed;
	}

	private static Device Resolve(string spec)
	{
		if (spec == "db")
		{
			return _housing;
		}
		Device found;
		if (!_devices.TryGetValue(spec, out found))
		{
			throw new ArgumentException("no device at " + spec);
		}
		return found;
	}

	// Interactable states are written as the raw ints, bypassing SetLogicValue,
	// so a run can put a device in a state no logic write could reach — a mode a
	// prefab does not name, an error nobody raised.
	private static void SetValue(Device device, LogicType logicType, double value)
	{
		switch (logicType)
		{
		case LogicType.Color:
		case LogicType.Activate:
		case LogicType.On:
		case LogicType.Power:
		case LogicType.Error:
		case LogicType.Mode:
		case LogicType.Open:
		case LogicType.Lock: Interactable(device, logicType).State = (int)value; break;
		case LogicType.PrefabHash: device.PrefabHash = (int)value; break;
		case LogicType.NameHash: device.NameHash = (int)value; break;
		case LogicType.ReferenceId: device.ReferenceId = (long)value; break;
		case LogicType.RequiredPower: device.UsedPower = (float)value; break;
		case LogicType.Setting: Housing(device).Setting = value; break;
		case LogicType.Combustion: Atmos(device).Sparked = value != 0.0; break;
		case LogicType.TotalMoles: Atmos(device).TotalMoles = value; break;
		case LogicType.Pressure: Atmos(device).PressureGassesAndLiquids = value; break;
		case LogicType.Temperature: Atmos(device).Temperature = value; break;
		default: throw new ArgumentException("no state behind LogicType." + logicType + "; a gas ratio is set with ratio, a stack size is the chip's");
		}
	}

	private static Interactable Interactable(Device device, LogicType logicType)
	{
		switch (logicType)
		{
		case LogicType.Color: return device.InteractColor;
		case LogicType.Activate: return device.InteractActivate;
		case LogicType.On: return device.InteractOnOff;
		case LogicType.Power: return device.InteractPowered;
		case LogicType.Error: return device.InteractError;
		case LogicType.Mode: return device.InteractMode;
		case LogicType.Open: return device.InteractOpen;
		case LogicType.Lock: return device.InteractLock;
		default: throw new ArgumentException("no interactable is behind LogicType." + logicType);
		}
	}

	private static Atmosphere Atmos(Device device)
	{
		if (device.InternalAtmosphere == null)
		{
			device.InternalAtmosphere = new Atmosphere();
		}
		return device.InternalAtmosphere;
	}

	private static CircuitHousing Housing(Device device)
	{
		CircuitHousing housing = device as CircuitHousing;
		if (housing == null)
		{
			throw new ArgumentException("only a housing carries that; name the target db");
		}
		return housing;
	}

	// A housing with no data cable is refused rather than given one: a run that
	// never wired it is asking what an unwired housing does, and every batch
	// form faults on that already.
	private static CableNetwork DataNetwork()
	{
		if (_housing.InputNetwork1 == null)
		{
			throw new ArgumentException("the housing is on no data cable, so nothing is on its batch; " +
				"run \"dev db network 1\" first");
		}
		return _housing.InputNetwork1;
	}

	private static void SetFlag(Device device, string name, bool value)
	{
		switch (name)
		{
		case "IsStructureCompleted": device.IsStructureCompleted = value; break;
		case "HasColorState": device.HasColorState = value; break;
		case "HasActivateState": device.HasActivateState = value; break;
		case "HasPowerState": device.HasPowerState = value; break;
		case "HasOpenState": device.HasOpenState = value; break;
		case "HasModeState": device.HasModeState = value; break;
		case "HasErrorState": device.HasErrorState = value; break;
		case "HasLockState": device.HasLockState = value; break;
		case "HasOnOffState": device.HasOnOffState = value; break;
		case "HasReadableAtmosphere": device.HasReadableAtmosphere = value; break;
		case "HasReadableReagentMixture": device.HasReadableReagentMixture = value; break;
		case "HasAnySlots": device.HasAnySlots = value; break;
		default: throw new ArgumentException("unknown device flag " + name);
		}
	}

	// Registers are written in full; the stack, being 512 wide and almost always
	// empty, only where nonzero. A slot is omitted when its bit pattern is zero,
	// not when it compares equal to zero — the one case they differ is -0.0,
	// which must still be reported.
	private static void WriteState()
	{
		StringBuilder text = new StringBuilder();
		text.Append("regs");
		double[] registers = _chip.HarnessRegisters;
		for (int i = 0; i < registers.Length; i++)
		{
			text.Append(' ').Append(Bits(registers[i]));
		}
		text.Append('\n');

		double[] stack = _chip.HarnessStack;
		for (int i = 0; i < stack.Length; i++)
		{
			if (BitConverter.DoubleToInt64Bits(stack[i]) != 0L)
			{
				text.Append("stack ").Append(i.ToString(CultureInfo.InvariantCulture))
					.Append(' ').Append(Bits(stack[i])).Append('\n');
			}
		}

		text.Append("ip ").Append(_chip.HarnessNextAddress.ToString(CultureInfo.InvariantCulture)).Append('\n');
		text.Append("lines ").Append(_chip.HarnessLineCount.ToString(CultureInfo.InvariantCulture)).Append('\n');
		text.Append("err ").Append(_chip.HarnessErrorType.ToString())
			.Append(' ').Append(_chip.HarnessErrorLine.ToString(CultureInfo.InvariantCulture)).Append('\n');
		text.Append("cerr ").Append(_chip.HarnessCompileErrorType.ToString())
			.Append(' ').Append(_chip.HarnessCompileErrorLine.ToString(CultureInfo.InvariantCulture)).Append('\n');
		text.Append("housing ").Append(_housing.HarnessCodeErrorState.ToString(CultureInfo.InvariantCulture)).Append('\n');

		// Present only in a permissive process. A driver reading the game's own
		// devices has no arm for this key, so such a driver would fail on the
		// first state rather than silently treat every permission as granted.
		if (_fixtures)
		{
			text.Append("fixtures ").Append(_writes.Count.ToString(CultureInfo.InvariantCulture)).Append('\n');
		}
		text.Append("end");
		Console.WriteLine(text.ToString());
	}

	// Width is fixed so a truncated token is a refusal, not a value 16x too
	// small; the prefix keeps a bit pattern from being read as the decimal it resembles.
	private const string BitsPrefix = "0x";

	private const int BitsDigits = 16;

	private static string Bits(double value)
	{
		return BitsPrefix + BitConverter.DoubleToInt64Bits(value).ToString("x16", CultureInfo.InvariantCulture);
	}

	// Accepts exactly what Bits writes. Convert's own parser would take a short
	// token, upper case, a sign, or surrounding space — each reading as a value
	// rather than the protocol error it is.
	private static double ParseBits(string text)
	{
		if (text.Length != BitsPrefix.Length + BitsDigits || !text.StartsWith(BitsPrefix, StringComparison.Ordinal))
		{
			throw new ArgumentException(BitsExpected + text);
		}
		for (int i = BitsPrefix.Length; i < text.Length; i++)
		{
			char c = text[i];
			if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')))
			{
				throw new ArgumentException(BitsExpected + text);
			}
		}
		ulong bits = Convert.ToUInt64(text.Substring(BitsPrefix.Length), 16);
		return BitConverter.Int64BitsToDouble(unchecked((long)bits));
	}

	private const string BitsExpected = "a double is \"0x\" and 16 lower case hexadecimal digits, got ";

	private static T ParseEnum<T>(string text) where T : struct
	{
		T parsed;
		if (!Enum.TryParse<T>(text, out parsed))
		{
			throw new ArgumentException("no " + typeof(T).Name + " named " + text);
		}
		return parsed;
	}

	private static string Decode(string text)
	{
		return Encoding.UTF8.GetString(Convert.FromBase64String(text));
	}

	private static string Flatten(string text)
	{
		return text.Replace('\n', ' ').Replace('\r', ' ');
	}
}

// One write a fixture device recorded. Property is read against LogicType for
// a device write or LogicSlotType for a slot write, per Slot.
public struct FixtureWrite
{
	public int Pin;

	public int Property;

	public int Slot;

	public double Value;
}

// FixtureDevice answers every property a program asks for, rather than the
// ones a real prefab publishes, so a comparison run never stops at a gate a
// real device would apply. It derives from ReagentUser because two of lr's
// four modes need that interface.
public sealed class FixtureDevice : ReagentUser
{
	// Slots are numbered from zero, so no real slot collides with this.
	public const int NoSlot = -1;

	private readonly int _pin;

	private readonly List<FixtureWrite> _writes;

	private readonly Dictionary<LogicType, double> _values = new Dictionary<LogicType, double>();

	// Slot and property are packed into one key rather than held in a struct, so
	// nothing here needs to declare an equality for them.
	private readonly Dictionary<long, double> _slots = new Dictionary<long, double>();

	public FixtureDevice(int pin, List<FixtureWrite> writes)
	{
		_pin = pin;
		_writes = writes;
	}

	// Seed/SeedSlot set a reading without recording it: seeded state is the
	// world a program meets, not something it did.
	public void Seed(LogicType property, double value)
	{
		_values[property] = value;
	}

	public void SeedSlot(LogicSlotType property, int slot, double value)
	{
		_slots[SlotKey(slot, property)] = value;
	}

	public override bool CanLogicRead(LogicType logicType)
	{
		return true;
	}

	public override bool CanLogicWrite(LogicType logicType)
	{
		return true;
	}

	public override double GetLogicValue(LogicType logicType)
	{
		double value;
		return _values.TryGetValue(logicType, out value) ? value : 0.0;
	}

	public override void SetLogicValue(LogicType logicType, double value)
	{
		_values[logicType] = value;
		_writes.Add(new FixtureWrite { Pin = _pin, Property = (int)logicType, Slot = NoSlot, Value = value });
	}

	public override bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		return true;
	}

	public override bool CanLogicWrite(LogicSlotType logicSlotType, int slotId)
	{
		return true;
	}

	public override double GetLogicValue(LogicSlotType logicSlotType, int slotId)
	{
		double value;
		return _slots.TryGetValue(SlotKey(slotId, logicSlotType), out value) ? value : 0.0;
	}

	public override void SetLogicValue(LogicSlotType logicSlotType, int slotId, double value)
	{
		_slots[SlotKey(slotId, logicSlotType)] = value;
		_writes.Add(new FixtureWrite { Pin = _pin, Property = (int)logicSlotType, Slot = slotId, Value = value });
	}

	// Injective over both operands: a slot index is whatever the program
	// computed and property is an int-backed enum, so neither half can carry into the other.
	private static long SlotKey(int slot, LogicSlotType property)
	{
		return ((long)slot << 32) | (uint)(int)property;
	}
}
