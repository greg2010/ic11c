using System.Xml.Serialization;

namespace Assets.Scripts.Objects;

public class Slot
{
	public enum Class : ushort
	{
		[XmlEnum("None")]
		None = 0,
		[XmlEnum("Helmet")]
		Helmet = 0x1,
		[XmlEnum("Suit")]
		Suit = 2u
	}
}
