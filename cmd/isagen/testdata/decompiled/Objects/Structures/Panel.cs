using Assets.Scripts;
using Assets.Scripts.Objects.Motherboards;
using Assets.Scripts.Objects.Pipes;

namespace Objects.Structures;

public class Panel : Device
{
	private string[] _modeStrings;

	public override string[] ModeStrings => _modeStrings;

	public override bool CanLogicRead(LogicType logicType)
	{
		if (logicType - 1 <= LogicType.Power)
		{
			return true;
		}
		return base.CanLogicRead(logicType);
	}

	public override bool CanLogicWrite(LogicType logicType)
	{
		if (logicType == LogicType.Open)
		{
			return true;
		}
		return base.CanLogicRead(logicType);
	}
}
