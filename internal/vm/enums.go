package vm

import (
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// scriptEnumResolver is one entry of ProgrammableChip.InternalEnums.
//
// The list has two shapes. A ScriptEnum matches a bare member name, but only
// when the operand carries the matching include flag, which is how a bare
// `Temperature` is a logic type in the logic type position and not a number
// anywhere else. A BasicEnum matches a `Type.Member` spelling whenever the
// operand allows enums at all, and one entry has no type prefix, so its members
// match bare in every value position.
type scriptEnumResolver struct {
	// include is the flag that gates a ScriptEnum. Zero marks a BasicEnum.
	include instructionInclude
	// prefix is the `Type` part a BasicEnum requires, compared case
	// insensitively. Empty means the members match bare.
	prefix string
	lookup func(name string) (int, bool)
}

// scriptEnums mirrors ProgrammableChip.InternalEnums in declaration order.
// Order decides which table wins when two enums share a member name, because
// the first match stops the search.
var scriptEnums = buildScriptEnums()

func buildScriptEnums() []scriptEnumResolver {
	resolvers := []scriptEnumResolver{
		{include: includeLogicType, lookup: lookupLogicTypeValue},
		{include: includeLogicSlotType, lookup: lookupLogicSlotTypeValue},
		{include: includeLogicReagentMode, lookup: lookupReagentModeValue},
		{include: includeLogicBatchMethod, lookup: lookupBatchModeValue},
		{prefix: "LogicType", lookup: lookupLogicTypeValue},
		{prefix: "LogicSlotType", lookup: lookupLogicSlotTypeValue},
	}
	for _, table := range basicEnumMembers {
		resolvers = append(resolvers, scriptEnumResolver{prefix: table.name, lookup: memberLookup(table.members)})
	}
	return resolvers
}

func memberLookup(members map[string]int) func(string) (int, bool) {
	return func(name string) (int, bool) {
		value, ok := members[name]
		return value, ok
	}
}

func lookupLogicTypeValue(name string) (int, bool) {
	info, ok := ic10.LookupLogicType(name)
	return int(info.Value), ok
}

func lookupLogicSlotTypeValue(name string) (int, bool) {
	info, ok := ic10.LookupLogicSlotType(name)
	return int(info.Value), ok
}

func lookupReagentModeValue(name string) (int, bool) {
	info, ok := ic10.LookupReagentMode(name)
	return int(info.Value), ok
}

func lookupBatchModeValue(name string) (int, bool) {
	info, ok := ic10.LookupBatchMode(name)
	return int(info.Value), ok
}

// resolveScriptEnum walks InternalEnums the way both value-carrying operand
// constructors do, and reports the first match.
func resolveScriptEnum(code string, props instructionInclude) (int, bool) {
	for _, resolver := range scriptEnums {
		if resolver.include != 0 {
			if props&resolver.include == 0 {
				continue
			}
			if value, ok := resolver.lookup(code); ok {
				return value, true
			}
			continue
		}
		if props&includeEnum == 0 {
			continue
		}
		name := code
		if resolver.prefix != "" {
			prefix, member, found := strings.Cut(code, ".")
			if !found || strings.Contains(member, ".") || !strings.EqualFold(prefix, resolver.prefix) {
				continue
			}
			name = member
		}
		if value, ok := resolver.lookup(name); ok {
			return value, true
		}
	}
	return 0, false
}

// lookupConstant resolves one of the nine named numeric constants. The chip
// compares these names case insensitively, unlike every other name in the
// language.
func lookupConstant(code string) (float64, bool) {
	if code == "" {
		return 0, false
	}
	constant, ok := ic10.LookupConstant(code)
	return constant.Value, ok
}
