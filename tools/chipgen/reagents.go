package main

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	reagentPath        = "Reagents/Reagent.cs"
	organicReagentPath = "Reagents/OrganicReagent.cs"
	reagentMixturePath = "Reagents/ReagentMixture.cs"
	recipePath         = "Reagents/Recipe.cs"
)

// reagentMembers is the lookup path _LR_Operation and _RMAP_Operation
// reach, and nothing else. Reagent is half lookup table and half
// simulation participant — burns, carries heat, notifies its mixture,
// serializes — and only the lookup half is kept.
var reagentMembers = []string{
	"public byte ReagentId",
	"private static Reagent[] _reagentLookup",
	"private static Dictionary<int, Reagent> _reagentHashLookup",
	"public string TypeNameShort",
	"public ReagentMixture ParentMixture",
	"public string TypeName",
	"public string Unit",
	"private double _quantity",
	"public float SpecificHeat",
	"public readonly int Hash",
	"public static List<Reagent> AllReagents",
	"public double Quantity",
	"public static implicit operator double(Reagent reagent)",
	"static Reagent()",
	"public static void GenerateReagentTypeLookup()",
	"public static Reagent Find(int hash)",
	"public static Reagent Find(string value)",
	"public Reagent()",
	"public Reagent(double quantity)",
	"public virtual void Burn()",
	"public bool Equals(Reagent reagent)",
	"public override bool Equals(object obj)",
	"public override int GetHashCode()",
}

// quantityNotification is the block the Quantity setter runs after storing a
// value. It compares the mixture's owner against null through the Unity object
// lifetime operator and then tells the world the mixture changed. Nothing here
// owns a mixture, and the stored value is the same either way.
const quantityNotification = "if (ParentMixture != null"

// sortedRosterStatement builds a display-ordered copy of the roster. The
// ordering is by localized name, which is a string table this unit does not
// lift, and nothing on the lookup path reads it.
const sortedRosterStatement = "AllReagentsSorted = "

// rosterEntry matches one constructor call in the AllReagents initializer.
var rosterEntry = regexp.MustCompile(`new (\w+)\(`)

// sliceReagents renders the reagent lookup the reagent instructions read
// through. The roster is read out of Reagent's own static constructor
// rather than a list here, so a new reagent's class, mixture field and
// recipe field are all picked up, and a renamed one stops the slice at the field no longer found.
func sliceReagents(s *slicing) (string, error) {
	src, err := s.read(reagentPath)
	if err != nil {
		return "", err
	}
	reagent, err := src.topLevelType("Reagent")
	if err != nil {
		return "", err
	}

	roster, err := reagentRoster(src, reagent)
	if err != nil {
		return "", err
	}

	body, err := liftReagentMembers(src, reagent)
	if err != nil {
		return "", err
	}
	parts := []string{"// " + src.rel + ", reduced to the lookup path\npublic class Reagent : IEquatable<Reagent>\n{\n" + body + "\n}"}

	organic, err := s.read(organicReagentPath)
	if err != nil {
		return "", err
	}
	organicType, err := organic.topLevelType("OrganicReagent")
	if err != nil {
		return "", err
	}
	if err := organic.top().cutTree(organicType); err != nil {
		return "", err
	}
	parts = append(parts, organic.top().lift(organicType, ""))

	for _, name := range roster {
		file, err := s.read("Reagents/" + name + ".cs")
		if err != nil {
			return "", err
		}
		concrete, err := file.topLevelType(name)
		if err != nil {
			return "", err
		}
		if len(concrete.bases) != 1 || (concrete.bases[0] != "Reagent" && concrete.bases[0] != "OrganicReagent") {
			return "", fmt.Errorf("%s: %s derives from %q, expected Reagent or OrganicReagent",
				file.rel, name, strings.Join(concrete.bases, ", "))
		}
		if err := file.top().cutTree(concrete); err != nil {
			return "", err
		}
		parts = append(parts, s.strip(file.top().lift(concrete, "")))
	}

	for _, container := range reagentContainers {
		text, err := sliceReagentContainer(s, container, roster)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}

	return reagentHeader + strings.Join(parts, "\n\n") + "\n", nil
}

// reagentContainer is one of the two per-reagent tables the reagent
// instructions read through: the mixture a device holds and the recipe it is
// fed.
type reagentContainer struct {
	path, name string
	// field renders the declaration of one reagent's member, which is what the
	// lifted class must still declare for the roster to be accounted for.
	field func(reagent string) string
	// assign renders the statement that puts a quantity into that member, which
	// is what the injected setter is built out of.
	assign func(reagent string) string
	// extras are the members lifted alongside the per-reagent ones.
	extras []string
}

var reagentContainers = []reagentContainer{
	{
		path: reagentMixturePath, name: "ReagentMixture",
		field:  func(reagent string) string { return "public " + reagent + " " + reagent },
		assign: func(reagent string) string { return reagent + ".Quantity = quantity;" },
		extras: []string{"public double TotalReagents", "public double Get(Reagent reagent)"},
	},
	{
		path: recipePath, name: "Recipe",
		field:  func(reagent string) string { return "public double " + reagent },
		assign: func(reagent string) string { return reagent + " = quantity;" },
		extras: []string{"public double Get(Reagent reagent)"},
	},
}

const reagentHeader = `using System;
using System.Collections.Generic;

`

// reagentRoster reads the reagent classes named by Reagent's static
// constructor, in the order it names them.
func reagentRoster(src *sourceFile, reagent decl) ([]string, error) {
	ctor, err := src.top().scopeOf(reagent).member("static Reagent()")
	if err != nil {
		return nil, fmt.Errorf("%s: Reagent.%w", src.rel, err)
	}
	statement, err := statementFor(ctor.text, "AllReagents = ")
	if err != nil {
		return nil, fmt.Errorf("%s: Reagent static constructor: %w", src.rel, err)
	}
	matches := rosterEntry.FindAllStringSubmatch(statement, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s: Reagent static constructor names no reagents: %w", src.rel, errNotFound)
	}
	roster := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		if seen[m[1]] {
			return nil, fmt.Errorf("%s: reagent %s appears twice in the roster", src.rel, m[1])
		}
		seen[m[1]] = true
		roster = append(roster, m[1])
	}
	return roster, nil
}

func liftReagentMembers(src *sourceFile, reagent decl) (string, error) {
	body := src.top().scopeOf(reagent)
	decls, err := splitDecls(body.body)
	if err != nil {
		return "", fmt.Errorf("%s: split Reagent body: %w", src.rel, err)
	}
	wanted := make(map[string]bool, len(reagentMembers))
	for _, sig := range reagentMembers {
		wanted[sig] = true
	}
	found := make(map[string]bool, len(reagentMembers))
	var parts []string
	for _, d := range decls {
		if !wanted[d.name] || found[d.name] {
			continue
		}
		found[d.name] = true
		if err := body.cutTree(d); err != nil {
			return "", err
		}
		text := src.slice.strip(d.text)
		switch d.name {
		case "public double Quantity":
			text, err = cutBlock(text, quantityNotification)
			if err != nil {
				return "", fmt.Errorf("%s: Reagent.Quantity: %w", src.rel, err)
			}
		case "static Reagent()":
			text, err = cutStatement(text, sortedRosterStatement)
			if err != nil {
				return "", fmt.Errorf("%s: Reagent static constructor: %w", src.rel, err)
			}
			text, err = cutStatement(text, "AllReagentsSorted.")
			if err != nil {
				return "", fmt.Errorf("%s: Reagent static constructor: %w", src.rel, err)
			}
		}
		parts = append(parts, nest(text))
	}
	var missing []string
	for _, sig := range reagentMembers {
		if !found[sig] {
			missing = append(missing, sig)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%s: Reagent no longer declares:\n\t%s\n%w",
			src.rel, strings.Join(missing, "\n\t"), errNotFound)
	}
	return strings.Join(parts, "\n\n"), nil
}

// sliceReagentContainer renders one of the two per-reagent tables. Both
// are the same shape — one member per reagent plus a lookup mapping a
// reagent instance onto its member — and both are lifted rather than
// generated, because the lookup is a chain of type tests whose order is the game's.
func sliceReagentContainer(s *slicing, container reagentContainer, roster []string) (string, error) {
	src, err := s.read(container.path)
	if err != nil {
		return "", err
	}
	typeDecl, err := src.topLevelType(container.name)
	if err != nil {
		return "", err
	}
	body := src.top().scopeOf(typeDecl)
	decls, err := splitDecls(body.body)
	if err != nil {
		return "", fmt.Errorf("%s: split %s body: %w", src.rel, container.name, err)
	}
	byName := make(map[string]decl, len(decls))
	for _, d := range decls {
		byName[d.name] = d
	}

	var parts, missing []string
	for _, reagent := range roster {
		signature := container.field(reagent)
		d, ok := byName[signature]
		if !ok {
			missing = append(missing, signature)
			continue
		}
		if err := body.cutTree(d); err != nil {
			return "", err
		}
		parts = append(parts, nest(s.strip(d.text)))
	}
	for _, signature := range container.extras {
		d, ok := byName[signature]
		if !ok {
			missing = append(missing, signature)
			continue
		}
		if err := body.cutTree(d); err != nil {
			return "", err
		}
		parts = append(parts, "\t// verbatim: "+body.span(d)+"\n"+nest(s.strip(d.text)))
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%s: %s no longer declares:\n\t%s\n%w",
			src.rel, container.name, strings.Join(missing, "\n\t"), errNotFound)
	}
	parts = append(parts, reagentSetter(container, roster))

	return fmt.Sprintf("// %s, reduced to the %d reagent members and the lookup over them\npublic %s %s\n{\n%s\n}",
		src.rel, len(roster), typeDecl.keyword, container.name, strings.Join(parts, "\n\n")), nil
}

// reagentSetterDoc is what the injected setter says about itself.
const reagentSetterDoc = `	// Injected: the write side of the lookup above. The game fills these from
	// the world; a harness has neither, so it seeds through this instead of the
	// member, dispatching on the reagent's type the same chain Get uses. It
	// answers whether the reagent was known, since Get answers 0.0 either way and a caller could not otherwise tell a miss from a landed zero.`

// reagentSetter renders the injected per-reagent setter, mirroring the lifted
// Get: one arm per reagent, in roster order, dispatched on the reagent's type.
func reagentSetter(container reagentContainer, roster []string) string {
	var b strings.Builder
	b.WriteString(reagentSetterDoc)
	b.WriteString("\n\tpublic bool HarnessSet(Reagent reagent, double quantity)\n\t{\n")
	for _, reagent := range roster {
		fmt.Fprintf(&b, "\t\tif (reagent is %s)\n\t\t{\n\t\t\t%s\n\t\t\treturn true;\n\t\t}\n",
			reagent, container.assign(reagent))
	}
	b.WriteString("\t\treturn false;\n\t}")
	return b.String()
}

// cutBlock removes the statement beginning with prefix together with the brace
// block that follows it.
func cutBlock(src, prefix string) (string, error) {
	start := strings.Index(src, prefix)
	if start < 0 {
		return "", fmt.Errorf("block %q: %w", prefix, errNotFound)
	}
	if strings.Contains(src[start+len(prefix):], prefix) {
		return "", fmt.Errorf("block %q appears more than once", prefix)
	}
	_, end, err := matchDelim(src[start:], 0, '{', '}')
	if err != nil {
		return "", fmt.Errorf("block %q: %w", prefix, err)
	}
	return trimBlankRun(src[:lineStart(src, start)] + src[start+end+1:]), nil
}
