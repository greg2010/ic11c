namespace Assets.Scripts;

public static class EnumCollections
{
	public static readonly EnumCollection<PanelMode, byte> PanelModes = new EnumCollection<PanelMode, byte>(toProper: false);
}
