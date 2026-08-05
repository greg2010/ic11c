using System;
using System.Collections.Generic;

namespace Reagents;

[Serializable]
public class Reagent : IEquatable<Reagent>
{
	public byte ReagentId;

	public static List<Reagent> AllReagents;

	public readonly int Hash;

	static Reagent()
	{
		AllReagents = new List<Reagent>
		{
			new Flour(0.0),
			new Milk(0.0),
			new Iron(0.0)
		};
	}

	public static Reagent Generate(byte reagentId, float quantity = 0f)
	{
		return reagentId switch
		{
			0 => new Flour(quantity),
			1 => new Milk(quantity),
			2 => new Iron(quantity),
			_ => null,
		};
	}
}
