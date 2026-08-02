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
		case ScriptCommand.hcf:
			return MakeString(command, Color.White, 1);
		}
		return string.Empty;
	}
}
