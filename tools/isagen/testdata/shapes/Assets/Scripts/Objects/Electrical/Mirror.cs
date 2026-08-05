using Assets.Scripts.Objects.Motherboards;
using Assets.Scripts.Objects.Pipes;

namespace Assets.Scripts.Objects.Electrical;

public class Mirror : Device
{
	public Device CurrentDevice;

	public override bool CanLogicRead(LogicType logicType)
	{
		if (CurrentDevice == null)
		{
			return false;
		}
		return CurrentDevice.CanLogicRead(logicType);
	}
}
