namespace Assets.Scripts.Objects;

public class Thing
{
	public static string[] DefaultModeStrings = new string[2] { "Mode0", "Mode1" };

	public string PrefabName;

	public int PrefabHash;

	public List<Slot> Slots;

	public virtual bool HasReadableAtmosphere => false;

	public virtual string[] ModeStrings => DefaultModeStrings;
}
