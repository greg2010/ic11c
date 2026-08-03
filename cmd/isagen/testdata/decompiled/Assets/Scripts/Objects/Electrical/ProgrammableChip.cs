using System;
using System.Collections.Generic;
using Assets.Scripts.Objects.Motherboards;

namespace Assets.Scripts.Objects.Electrical;

public class ProgrammableChip
{
	public static Constant[] AllConstants;

	public static List<IScriptEnum> InternalEnums;

	static ProgrammableChip()
	{
		REGISTER = new HelpString("r?", Color.Blue);
		NUMBER = new HelpString("num", Color.Green);
		LOGIC_TYPE = new HelpString("logicType", Color.Red);
		AllConstants = new Constant[2]
		{
			new Constant("nan", "Not a number", double.NaN),
			new Constant("pi", "Ratio of a circle's circumference to its diameter", Math.PI)
		};
		InternalEnums = new List<IScriptEnum>
		{
			new ScriptEnum<LogicType>(InstructionInclude.LogicType, LogicBase.IsDeprecated, LogicBase.GetLogicDescription),
			new BasicEnum<LogicType>("LogicType", LogicBase.IsDeprecated),
			new BasicEnum<ColorType>("Color"),
			new BasicEnum<Slot.Class>("SlotClass"),
			new BasicEnum<ConditionOperation>()
		};
	}

	public string GetCommandExample(ScriptCommand command)
	{
		switch (command)
		{
		case ScriptCommand.move:
			return MakeString(command, Color.White, 1, REGISTER, (REGISTER + NUMBER).Var("a"));
		case ScriptCommand.l:
			return MakeString(command, Color.White, 1, REGISTER, LOGIC_TYPE);
		case ScriptCommand.swap:
			return MakeString(command, Color.White, 1, (REGISTER + NUMBER).Var("a"), REGISTER);
		case ScriptCommand.hcf:
			return MakeString(command, Color.White, 1);
		}
		return string.Empty;
	}

	private class _LineOfCode
	{
		public readonly _Operation Operation;

		public _LineOfCode(ProgrammableChip chip, string lineOfCode, int lineNumber)
		{
			string[] array = lineOfCode.Split();
			if (array.Length == 0)
			{
				Operation = new _NOOP_Operation(chip, lineNumber);
			}
			else
			{
				switch ((ScriptCommand)Enum.Parse(typeof(ScriptCommand), array[0]))
				{
				case ScriptCommand.move:
					Operation = new _MOVE_Operation(chip, lineNumber, array[1], array[2]);
					break;
				case ScriptCommand.l:
					Operation = new _L_Operation(chip, lineNumber, array[1], array[2]);
					break;
				case ScriptCommand.swap:
					Operation = new _SWAP_Operation(chip, lineNumber, array[1], array[2]);
					break;
				case ScriptCommand.hcf:
					Operation = new _HCF_Operation(chip, lineNumber);
					break;
				}
			}
		}
	}

	private abstract class _Operation
	{
		protected readonly ProgrammableChip _Chip;

		protected readonly int _LineNumber;

		public _Operation(ProgrammableChip chip, int lineNumber)
		{
			_Chip = chip;
			_LineNumber = lineNumber;
		}

		public abstract int Execute(int index);
	}

	private abstract class _Operation_1_0 : _Operation
	{
		protected IndexVariable _Store;

		public _Operation_1_0(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber)
		{
			_Store = new IndexVariable(chip, lineNumber, registerStoreCode, InstructionInclude.MaskStoreIndex, throwException: false);
		}
	}

	private abstract class _Operation_1_1 : _Operation_1_0
	{
		protected DoubleValueVariable _Argument1;

		public _Operation_1_1(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode)
		{
			_Argument1 = new DoubleValueVariable(chip, lineNumber, argument1Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}
	}

	private class _MOVE_Operation : _Operation_1_1
	{
		public _MOVE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string registerArgument1Code)
			: base(chip, lineNumber, registerStoreCode, registerArgument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = variableValue;
			return index + 1;
		}
	}

	private class _L_Operation : _Operation_1_0
	{
		protected readonly EnumValuedVariable<LogicType> _LogicType;

		public _L_Operation(ProgrammableChip chip, int lineNumber, string registerCode, string logicTypeCode)
			: base(chip, lineNumber, registerCode)
		{
			_LogicType = new EnumValuedVariable<LogicType>(chip, lineNumber, logicTypeCode, InstructionInclude.LogicType, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			LogicType variableValue = _LogicType.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetLogicValue(variableValue);
			return index + 1;
		}
	}

	// swap is in no shipped build. It stands in for an instruction a future one
	// could introduce: an operation whose store is its second operand, which the
	// shape of the operand list gives no way to tell from an instruction that
	// writes nothing at all.
	private class _SWAP_Operation : _Operation_1_1
	{
		public _SWAP_Operation(ProgrammableChip chip, int lineNumber, string registerArgument1Code, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode, registerArgument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = variableValue;
			return index + 1;
		}
	}

	private class _HCF_Operation : _Operation
	{
		public _HCF_Operation(ProgrammableChip chip, int lineNumber)
			: base(chip, lineNumber)
		{
		}

		public override int Execute(int index)
		{
			_Chip.HaltAndCatchFire();
			throw new ProgrammableChipException(ProgrammableChipException.ICExceptionType.ChipCatchingFire, _LineNumber);
		}
	}

	private class _NOOP_Operation : _Operation
	{
		public _NOOP_Operation(ProgrammableChip chip, int lineNumber)
			: base(chip, lineNumber)
		{
		}

		public override int Execute(int index)
		{
			return index + 1;
		}
	}
}
