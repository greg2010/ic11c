using System.Collections.Generic;
using Assets.Scripts.Objects.Electrical;

namespace Assets.Scripts.Objects.Motherboards;

public class LogicBase
{
	public static List<ScriptCommand> DeprecatedCommands = new List<ScriptCommand> { ScriptCommand.hcf };

	public static List<LogicType> Deprecated = new List<LogicType> { LogicType.Mode };

	public static List<LogicSlotType> DeprecatedSlotTypes = new List<LogicSlotType>();
}
