package sema

import (
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// namedOperandKinds are the operand positions that resolve a machine name,
// in the order the families claim spellings. The order decides which family
// a shared name belongs to: the generated C prelude claims names in the
// same order, and internal/ic10's prelude test holds the two together.
var namedOperandKinds = [...]OperandKind{
	OperandLogicType, OperandSlotType, OperandBatchMode, OperandReagentMode,
}

// operandPrefixes is the spelling each family gives a name an earlier family
// already claimed, indexed by the position that gives it. MicroC resolves a
// machine name in one namespace, as C requires: a bare name means whatever
// the prelude declares it as, wherever it is written.
var operandPrefixes = [...]string{
	OperandLogicType:   ic10.LogicTypePrefix,
	OperandSlotType:    ic10.SlotTypePrefix,
	OperandBatchMode:   ic10.BatchModePrefix,
	OperandReagentMode: ic10.ReagentModePrefix,
}

// familyMember resolves name against the table one position draws from, and
// reports false for every kind that draws from none.
func (c *checker) familyMember(kind OperandKind, name string) (Member, bool) {
	switch kind {
	case OperandLogicType:
		return c.tables.LogicType(name)
	case OperandSlotType:
		return c.tables.LogicSlotType(name)
	case OperandBatchMode:
		return c.tables.BatchMode(name)
	case OperandReagentMode:
		return c.tables.ReagentMode(name)
	case OperandValue, OperandDouble, OperandSlot, OperandDevice, OperandString:
		return Member{}, false
	}
	return Member{}, false
}

// bareOwner reports which position owns the bare spelling of name: the first
// family carrying it, which is the one the prelude declares it under. A false
// answer means no family carries the name at all, in which case the kind means
// nothing.
func (c *checker) bareOwner(name string) (OperandKind, bool) {
	for _, kind := range namedOperandKinds {
		if _, ok := c.familyMember(kind, name); ok {
			return kind, true
		}
	}
	return OperandValue, false
}

// operandSpelling is what one position resolves an identifier as.
type operandSpelling uint8

const (
	// spellingUnknown is an identifier the position resolves to nothing.
	spellingUnknown operandSpelling = iota
	// spellingBare is the position's own member under the name the prelude
	// declares it by.
	spellingBare
	// spellingShadowed is a member of the position's family whose bare name
	// belongs to an earlier one. The prelude prefixes it, so the bare spelling
	// written here means the earlier family's member instead.
	spellingShadowed
	// spellingPrefixed is the position's own member under the prefixed name.
	spellingPrefixed
	// spellingOverPrefixed is a prefixed name for a member whose bare spelling
	// the position owns, which the prelude therefore never declares.
	spellingOverPrefixed
)

// resolveOperandName reads what one position makes of an identifier: the
// spelling it is, the member it names where it names one, and the base name a
// prefixed spelling was written over.
func (c *checker) resolveOperandName(kind OperandKind, name string) (operandSpelling, Member, string) {
	if member, ok := c.familyMember(kind, name); ok {
		owner, _ := c.bareOwner(name)
		if owner != kind {
			return spellingShadowed, member, name
		}
		return spellingBare, member, name
	}

	base, prefixed := strings.CutPrefix(name, operandPrefixes[kind])
	if !prefixed {
		return spellingUnknown, Member{}, ""
	}
	member, ok := c.familyMember(kind, base)
	if !ok {
		return spellingUnknown, Member{}, ""
	}
	if owner, _ := c.bareOwner(base); owner == kind {
		return spellingOverPrefixed, member, base
	}
	return spellingPrefixed, member, base
}
