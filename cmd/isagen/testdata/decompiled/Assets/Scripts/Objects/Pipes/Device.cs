using Assets.Scripts.Objects.Motherboards;

namespace Assets.Scripts.Objects.Pipes;

public class Device : Thing
{
	public float UsedPower = 10f;

	public virtual bool CanLogicRead(LogicType logicType)
	{
		if (!base.IsStructureCompleted && GameManager.GameState == GameState.Running)
		{
			return false;
		}
		switch (logicType)
		{
		case LogicType.Power:
			if (UsedPower > 0f)
			{
				return HasPowerState;
			}
			return false;
		case LogicType.Open:
			return HasReadableAtmosphere;
		case LogicType.Mode:
			return HasModeState;
		default:
			return false;
		}
	}

	public virtual bool CanLogicWrite(LogicType logicType)
	{
		if (!base.IsStructureCompleted && GameManager.GameState == GameState.Running)
		{
			return false;
		}
		return logicType switch
		{
			LogicType.Open => HasOpenState, 
			LogicType.Mode => HasModeState, 
			_ => false, 
		};
	}

	public virtual bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		Slot slot = GetSlot(slotId);
		if (slot == null)
		{
			return false;
		}
		switch (logicSlotType)
		{
		case LogicSlotType.Occupied:
			return true;
		case LogicSlotType.Quantity:
		{
			Slot.Class type = slot.Type;
			return type == Slot.Class.Helmet || type == Slot.Class.Suit;
		}
		default:
			return false;
		}
	}

	public virtual bool CanLogicWrite(LogicSlotType logicSlotType, int slotId)
	{
		if (!HasAnySlots || slotId < 0 || slotId >= Slots.Count)
		{
			return false;
		}
		if (logicSlotType == LogicSlotType.Quantity)
		{
			return Slots[slotId].Type == Slot.Class.Helmet;
		}
		return false;
	}
}
