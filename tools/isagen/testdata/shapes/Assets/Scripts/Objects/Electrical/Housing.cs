using System;
using Assets.Scripts.Objects.Motherboards;
using Assets.Scripts.Objects.Pipes;

namespace Assets.Scripts.Objects.Electrical;

public class Housing : Device, ICircuitHolder
{
	public override string[] ModeStrings => Enum.GetNames(typeof(HousingMode));

	public override bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		if (logicSlotType == LogicSlotType.Quantity)
		{
			return true;
		}
		return base.CanLogicRead(logicSlotType, slotId);
	}
}
