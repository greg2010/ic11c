package sema

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// hashIntrinsic is the intrinsic whose string literal a prefab operand is
// written through. Nothing else in the language turns a name into a hash.
const hashIntrinsic = reservedPrefix + "hash"

// noOperand marks a device form that has no operand of that kind.
const noOperand = -1

// deviceForm says where one device intrinsic's operands stand, which way it
// touches the device, and where the prefab it reaches comes from.
type deviceForm struct {
	dir Direction
	// pin says argument 0 is a device operand rather than a prefab hash, so the
	// prefab is whatever a declaration promised about that pin.
	pin bool
	// property is the argument naming the logic type, or the slot property
	// where slot is not noOperand. Every form has one: [deviceFormOf] builds
	// none for an intrinsic naming no property, since a property is what every
	// verdict the roster reaches is about.
	property int
	// slot is the argument carrying the slot index.
	slot int
	// value is the argument carrying what a store writes, which is where a Mode
	// number is read from.
	value int
}

// deviceDirections names every device intrinsic the prefab roster judges, and
// which way each one touches the device. Membership is a decision, not a
// derivation: an intrinsic can reach a device without the roster holding
// anything about what it reaches. See TestEveryDeviceIntrinsicIsRosterChecked.
var deviceDirections = map[string]Direction{
	"__ic_load":       Reading,
	"__ic_store":      Writing,
	"__ic_load_slot":  Reading,
	"__ic_store_slot": Writing,

	"__ic_load_batch":            Reading,
	"__ic_store_batch":           Writing,
	"__ic_load_batch_named":      Reading,
	"__ic_store_batch_named":     Writing,
	"__ic_load_batch_slot":       Reading,
	"__ic_store_batch_slot":      Writing,
	"__ic_load_batch_named_slot": Reading,
}

// deviceFormOf locates one intrinsic's operands in the parameter list that
// declares them, and reports false for an intrinsic the roster has no verdict
// to reach about. An intrinsic reaching a device need not name a property — a
// presence test or a reagent read does not — so a false form has none to read.
func deviceFormOf(in *Intrinsic, dir Direction) (deviceForm, bool) {
	property := operandIndex(in.Params, OperandLogicType, OperandSlotType)
	if property == noOperand {
		return deviceForm{}, false
	}
	return deviceForm{
		dir:      dir,
		pin:      len(in.Params) > 0 && in.Params[0] == OperandDevice,
		property: property,
		slot:     operandIndex(in.Params, OperandSlot),
		value:    operandIndex(in.Params, OperandDouble),
	}, true
}

// operandIndex is where the first parameter of any of the given kinds stands,
// and [noOperand] for a form carrying none of them.
func operandIndex(params []OperandKind, kinds ...OperandKind) int {
	for i, param := range params {
		if slices.Contains(kinds, param) {
			return i
		}
	}
	return noOperand
}

// deviceCall is one device instruction held until the whole file has been read.
// Neither prefab is settled at the call: what a variable standing in a batch
// operand holds is not decided until every write to it has been seen, and a
// declaration naming what a pin reaches can sit below the function that uses it.
type deviceCall struct {
	call *ast.CallExpr
	form deviceForm
}

// noteDeviceCall records a batch or pin instruction for
// [checker.checkDeviceSurfaces], and ignores every other intrinsic.
func (c *checker) noteDeviceCall(x *ast.CallExpr, in *Intrinsic) {
	dir, judged := deviceDirections[in.Name]
	if !judged {
		return
	}
	if form, reachable := deviceFormOf(in, dir); reachable {
		c.accesses = append(c.accesses, deviceCall{call: x, form: form})
	}
}

// checkDeviceSurfaces holds every device access to the prefab roster. It runs
// once the file has been read, for the reason [deviceCall] gives.
func (c *checker) checkDeviceSurfaces() {
	for _, access := range c.accesses {
		c.checkDeviceCall(access)
	}
}

// deviceSite is one device access with everything the checks read already
// resolved.
type deviceSite struct {
	call *ast.CallExpr
	form deviceForm
	// name is the intrinsic's own name, which every message quotes.
	name   string
	args   []Operand
	prefab Prefab
	// pin is the device the access reaches, and stands for nothing where
	// [deviceForm.pin] is false: a batch form reaches every device on the
	// network under the hash it carries rather than one position.
	pin Device
}

// arg is where the operand at index i was written.
func (s deviceSite) arg(i int) source.Position { return s.call.Args[i].Pos() }

// reach describes what the instruction touches and what a refusal costs
// there. A batch form names its devices, so a refusal faults wherever the
// network holds one; a pin form names a position the world fills, so it
// faults only where the world filled it the way the declaration says.
func (s deviceSite) reach() string {
	verb := "reads it from"
	if s.form.dir == Writing {
		verb = "writes it to"
	}
	if !s.form.pin {
		return fmt.Sprintf("%s %s every one the network holds and the chip faults on the first", s.name, verb)
	}
	return fmt.Sprintf("this program declares %s to be a %s, and %s %s whatever the world put there, so the chip faults on it wherever the declaration holds",
		s.pin, s.prefab.Name(), s.name, verb)
}

// slotFault says where a slot index the device does not declare faults, which
// follows the same division [deviceSite.reach] draws.
func (s deviceSite) slotFault() string {
	if !s.form.pin {
		return "it faults on the first one the network holds"
	}
	return fmt.Sprintf("it faults on whatever the world put on %s wherever this program's declaration of it holds", s.pin)
}

func (c *checker) checkDeviceCall(access deviceCall) {
	resolved := c.prog.Intrinsics[access.call]
	// An arity the call got wrong is already reported, and the operands past
	// the mistake stand for nothing.
	if resolved == nil || len(resolved.Args) != len(resolved.Intrinsic.Params) {
		return
	}

	prefab, pin, decided := c.sitePrefab(access, resolved.Intrinsic.Name)
	if !decided {
		return
	}
	site := deviceSite{
		call:   access.call,
		form:   access.form,
		name:   resolved.Intrinsic.Name,
		args:   resolved.Args,
		prefab: prefab,
		pin:    pin,
	}
	if access.form.slot == noOperand {
		c.checkDeviceLogic(site)
		return
	}
	c.checkDeviceSlot(site)
}

// sitePrefab answers the prefab one access reaches, the position it reaches
// it through where the form addresses one, and false where nothing is
// settled here. A batch form addresses no position and gets the zero device,
// which [deviceSite.pin] treats as standing for nothing.
func (c *checker) sitePrefab(access deviceCall, in string) (Prefab, Device, bool) {
	operand := access.call.Args[0]
	if access.form.pin {
		claim, pin, declared := c.declaredPrefab(operand)
		if !declared {
			return nil, Device{}, false
		}
		return claim, pin, true
	}
	named, decided := c.resolvePrefab(operand)
	if !decided {
		return nil, Device{}, false
	}
	prefab, ships := c.lookupPrefab(named)
	if !ships {
		c.reportUnknownPrefab(operand.Pos(), in, named)
		return nil, Device{}, false
	}
	return prefab, Device{}, true
}

// checkDeviceLogic holds a device-level access to what the prefab answers for,
// and the mode number a store carries to the modes it has.
func (c *checker) checkDeviceLogic(site deviceSite) {
	property := site.args[site.form.property]
	if !property.Resolved {
		return
	}
	if site.prefab.RefusesLogic(int(property.Value), site.form.dir) {
		c.reportRefused(site, site.arg(site.form.property), "", property.Name)
		return
	}
	c.checkModeNumber(site, property)
}

// checkDeviceSlot holds a slotted access to the slots the prefab declares and to
// what that slot answers for.
func (c *checker) checkDeviceSlot(site deviceSite) {
	slot := site.args[site.form.slot]
	property := site.args[site.form.property]
	if !slot.Resolved || !property.Resolved {
		return
	}
	if slot.Value >= int64(site.prefab.NumSlots()) {
		c.warnf(site.arg(site.form.slot),
			"a completed %s declares %s, and %s addresses slot %d; the chip refuses the slot on the device rather than on the line, so %s",
			describePrefab(site.prefab), source.Plural(site.prefab.NumSlots(), "slot"), site.name, slot.Value, site.slotFault())
		return
	}
	if site.prefab.RefusesSlot(int(slot.Value), int(property.Value), site.form.dir) {
		where := "slot " + strconv.FormatInt(slot.Value, 10)
		c.reportRefused(site, site.arg(site.form.property), where, property.Name)
	}
}

// checkModeNumber holds a constant written to Mode to the settings the game
// gives the device. It says nothing where the extraction could not recover
// the mode names: a class that fills them at run time still has mode state,
// and the inherited two-element default is not what it ends up with.
func (c *checker) checkModeNumber(site deviceSite, property Operand) {
	if site.form.value == noOperand || !c.isModeProperty(property) {
		return
	}
	modes, known := site.prefab.NumModes()
	if !known || modes == 0 {
		return
	}
	v, fail := c.constEval(site.call.Args[site.form.value], arithmeticConst)
	if fail != nil {
		return
	}
	written := v.Num()
	selected, settled := selectedMode(written)
	if settled && selected >= 0 && int(selected) < modes {
		return
	}
	// The converted mode is named only when settled, so the message never
	// suggests a mode the device cannot actually reach.
	converted := ""
	if settled && float64(selected) != written {
		converted = fmt.Sprintf(", which the device converts to mode %d", selected)
	}
	c.warnf(site.arg(site.form.value),
		"%s writes %s to '%s'%s, and a completed %s has %s to select between, numbered from 0",
		site.name, v.String(), modeProperty, converted, describePrefab(site.prefab), source.Plural(modes, "mode"))
}

// selectedMode is the mode a device ends up on when the chip hands it a
// double, and reports false where nothing settles which mode that is.
// ECMA-335 leaves a NaN or an out-of-range whole part unspecified; the game
// runs Mono on x86-64, whose cvttsd2si likely answers -2^31 for all of them,
// but that is asserted only on the negative side, where it also matches what
// naive saturation would guess.
func selectedMode(v float64) (mode int32, settled bool) {
	whole := math.Trunc(v)
	switch {
	case math.IsNaN(whole), whole > math.MaxInt32:
		return 0, false
	case whole < math.MinInt32:
		return math.MinInt32, true
	}
	return int32(whole), true
}

// modeProperty is the device property whose value indexes a prefab's mode list.
const modeProperty = "Mode"

// isModeProperty reports whether the operand resolved to [modeProperty]. It
// compares the encoding the machine reads rather than the identifier the
// source wrote, since every other roster query is settled by the encoding
// too.
func (c *checker) isModeProperty(property Operand) bool {
	mode, ships := c.tables.LogicType(modeProperty)
	return ships && property.Resolved && property.Value == int64(mode.Value)
}

func (c *checker) reportUnknownPrefab(pos source.Position, in string, named prefabRef) {
	subject := fmt.Sprintf("the prefab hash %d, and this game build ships nothing under it", named.hash)
	if named.spelled {
		subject = fmt.Sprintf("the hash of %q, and this game build ships nothing under that name", named.name)
	}
	c.warnf(pos, "%s is given %s; a batch operand matching no device is not an error on the chip, so the instruction is emitted and reaches nothing",
		in, subject)
}

// reportRefused names a property a completed device answers nothing for. where
// is the slot the property belongs to, and is empty for a device-level one.
func (c *checker) reportRefused(site deviceSite, pos source.Position, where, property string) {
	subject := "a completed " + describePrefab(site.prefab)
	if where != "" {
		subject = where + " of " + subject
	}
	if site.form.dir == Writing {
		c.warnf(pos, "%s accepts no write of '%s'; %s — check the device's properties for the one that takes this setting",
			subject, property, site.reach())
		return
	}
	c.warnf(pos, "%s answers nothing for '%s'; %s — check the device's properties for the one that carries this reading",
		subject, property, site.reach())
}

// describePrefab names a prefab the way a diagnostic should: the name the
// program hashed, and the English title beside it where the game ships one,
// since a roster name such as StructureCableAnalysizer is not what the thing is
// called in the game.
func describePrefab(p Prefab) string {
	if p.Title() == "" {
		return p.Name()
	}
	return p.Name() + " (" + p.Title() + ")"
}

// prefabRef is what a batch instruction's first argument was found to name.
type prefabRef struct {
	// name is the string the program hashed, and spelled says the program wrote
	// it rather than a number. An empty name is a program that hashed the empty
	// string, which is a name the roster does not hold either.
	name    string
	spelled bool
	hash    int32
}

func (c *checker) lookupPrefab(ref prefabRef) (Prefab, bool) {
	if ref.spelled {
		return c.tables.PrefabNamed(ref.name)
	}
	return c.tables.Prefab(ref.hash)
}

// resolvePrefab reads a batch instruction's prefab operand, reporting false
// where nothing about it is settled at compile time. A false answer is not a
// complaint: the chip takes a register there, so a hash the program computes
// is a legitimate program this pass has nothing to say about.
func (c *checker) resolvePrefab(x ast.Expr) (prefabRef, bool) {
	if name, hashed := c.hashedName(x); hashed {
		return prefabRef{name: name, spelled: true}, true
	}
	v, fail := c.constEval(x, arithmeticConst)
	// The game hashes to a signed 32 bit number and compares a prefab operand
	// against one, so a constant outside that range settles nothing the roster
	// can answer.
	if fail != nil || v.Type.Kind() == Double || v.Int < math.MinInt32 || v.Int > math.MaxInt32 {
		return prefabRef{}, false
	}
	return prefabRef{hash: int32(v.Int)}, true
}

// pinClaim is what one declaration promised is wired to one device position.
type pinClaim struct {
	// name is the prefab the attribute spelled, and pos where it spelled it.
	// Both are kept for the second declaration of the same pin, which is
	// reported against the first.
	name string
	pos  source.Position
	// prefab is the roster entry the name resolved to, and nil for a name this
	// game build ships nothing under, which is reported where it was written.
	// A claim the roster cannot answer still occupies the pin, so a second
	// declaration of it is still a contradiction.
	prefab Prefab
}

// declarePrefab records what a dev declaration promised about the pin it
// names. The claim covers the whole program, not the declaration's scope:
// what is wired to a housing pin is one fact about the world, so a
// block-scope declaration narrows which lines name the object, not the pin.
func (c *checker) declarePrefab(attr *ast.PrefabAttr, device Device) {
	if attr == nil {
		return
	}
	if prev, claimed := c.pins[device]; claimed {
		if prev.name != attr.Name {
			c.errorf(attr.NamePos, "'%s' is declared to be a %s at %s, and one housing position reaches one device; the two cannot both be true of the same chip",
				device, prev.name, prev.pos)
		}
		return
	}
	prefab, ships := c.tables.PrefabNamed(attr.Name)
	switch {
	case !ships:
		c.warnf(attr.NamePos, "this game build ships nothing named \"%s\", so nothing is known about what '%s' reaches and no access through it is checked; the roster is one pinned build, and a later one may ship it",
			attr.Name, device)
	case device.Base:
		c.checkCircuitHolder(attr.NamePos, device, prefab)
	}
	c.pins[device] = pinClaim{name: attr.Name, pos: attr.NamePos, prefab: prefab}
}

// checkCircuitHolder holds a declaration of db to what the roster says can
// hold a chip at all. db is the housing this program's own chip is inserted
// into, so naming a prefab there is a claim the roster can decide — unlike
// every other pin promise. It stays silent where extraction left that undecided.
func (c *checker) checkCircuitHolder(pos source.Position, device Device, prefab Prefab) {
	if holds, known := prefab.HoldsCircuit(); !known || holds {
		return
	}
	c.warnf(pos, "'%s' is the housing this chip is inserted into, and a completed %s holds no programmable chip; the game cannot place this chip in one, so nothing this declaration says can be true of the housing the program is running in",
		device, describePrefab(prefab))
}

// declaredPrefab answers what the program promised is wired to the device the
// operand x named, the position it named, and false where nothing is
// settled. A false answer is the common case, not a complaint: a program
// that does not say what is on a pin has left nothing to check.
func (c *checker) declaredPrefab(x ast.Expr) (Prefab, Device, bool) {
	pin, fixed := c.prog.Devices[x]
	if !fixed {
		return nil, Device{}, false
	}
	claim, declared := c.pins[pin]
	return claim.prefab, pin, declared && claim.prefab != nil
}

// rejectPrefabAttr reports the prefab attribute written on a declaration that
// names no device position.
func (c *checker) rejectPrefabAttr(d *ast.VarDecl, typ *Type) {
	if d.Prefab == nil || typ.Kind() == Invalid {
		return
	}
	c.errorf(d.Prefab.Pos(), "'%s' has type %s, and the prefab attribute states which device a housing position is wired to; it belongs on a dev declaration",
		d.Name, typ)
}

// hashedName reads the string a __ic_hash call standing in the operand hashed,
// or the one a variable standing there was given at its declaration.
func (c *checker) hashedName(x ast.Expr) (string, bool) {
	switch x := x.(type) {
	case *ast.CallExpr:
		return c.hashLiteral(x)
	case *ast.Ident:
		sym := c.prog.Uses[x]
		// An object whose address was taken can be written through a pointer,
		// which is a write this pass does not see at all.
		if sym == nil || !sym.hashKnown || sym.hashVaries || sym.Addressed {
			return "", false
		}
		return sym.hashName, true
	default:
		return "", false
	}
}

// hashLiteral reads the string literal a __ic_hash call hashed.
func (c *checker) hashLiteral(x *ast.CallExpr) (string, bool) {
	call := c.prog.Intrinsics[x]
	if call == nil || call.Intrinsic.Name != hashIntrinsic || len(call.Args) != 1 || !call.Args[0].Resolved {
		return "", false
	}
	return call.Args[0].Name, true
}

// recordHashedName remembers the string a declaration's initializer hashed,
// which is what lets a batch operand naming the object be checked. Only an
// initializer is recorded: an assignment marks the object varying instead,
// since tracking which write reaches a use takes data flow this pass lacks.
func (c *checker) recordHashedName(sym *Symbol, init ast.Expr) {
	call, isCall := init.(*ast.CallExpr)
	if !isCall {
		return
	}
	if name, hashed := c.hashLiteral(call); hashed {
		sym.hashName, sym.hashKnown = name, true
	}
}

// noteHashOverwritten records that a write other than the declaration's
// initializer reached the object, after which what it holds at a use is
// undecided.
func (c *checker) noteHashOverwritten(target ast.Expr) {
	id, named := target.(*ast.Ident)
	if !named {
		return
	}
	if sym := c.prog.Uses[id]; sym != nil {
		sym.hashVaries = true
	}
}
