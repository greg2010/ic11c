// Package sema checks a parsed MicroC translation unit and produces a typed
// program.
//
// The parser is syntax only, so every rule that needs a type, a name, a
// constant, or a notion of reachability is enforced here: name resolution and
// scoping, the type system and its conversions, switch fallthrough and case
// labels, the constant expressions array bounds and global initializers
// require, const, the intrinsics and the machine names their arguments carry,
// and the source-level half of the pointer restriction.
//
// Analysis collects diagnostics rather than stopping at the first one, and an
// expression it could not type takes the Invalid type, which absorbs every
// further operation silently. One mistake therefore produces one message.
//
// The pointer restriction is verified again after optimization, because
// optimization can introduce pointer merges the source never wrote. What this
// package rejects is the visible half: a merge written in the source.
package sema

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// maxDiagnostics caps how many problems one analysis reports. Past this point
// the output is noise rather than information.
const maxDiagnostics = 64

// Program is a checked translation unit. Every map is keyed by the syntax node
// the parser produced, so a later phase walks the same tree and looks up what
// analysis decided about each node.
//
// A Program returned alongside a non-empty diagnostic list is partial: names
// that did not resolve are absent from Uses and expressions that did not type
// are Invalid in Types. Only a program that analyzed cleanly is fit to lower.
type Program struct {
	File *ast.File
	// Globals holds every file-scope variable in declaration order.
	Globals []*Symbol
	// Funcs holds every function in declaration order, each carrying its call
	// edges and whether it is recursive.
	Funcs []*Func
	// Main is the entry point, or nil when the program declared no function
	// named main. It is set as soon as that name resolves, before the
	// signature and body checks run, so a non-nil Main may still carry a
	// rejected signature or a nil Decl. Only a clean diagnostic list makes it
	// usable.
	Main *Func

	// Types holds the type of every expression that denotes a value. A callee
	// name, a logic-type name, and the string literal __ic_hash takes denote no
	// value and are absent; what they resolved to is in Intrinsics instead. A
	// device name is present and has type dev.
	Types map[ast.Expr]*Type
	// Uses maps every identifier that resolved to the symbol it named.
	Uses map[*ast.Ident]*Symbol
	// Consts holds the folded value of every expression the language required
	// to be constant: array bounds, case labels, global initializers, brace
	// initializer elements, and constant intrinsic operands.
	Consts map[ast.Expr]Value
	// Conversions records the type an expression is implicitly converted to,
	// which happens on assignment, initialization, argument passing, return,
	// and in a condition. A conversion to bool normalizes to 0 or 1.
	Conversions map[ast.Expr]*Type
	// Calls maps every call of a declared function to the function called.
	Calls map[*ast.CallExpr]*Func
	// Intrinsics maps every intrinsic call to its resolved operands.
	Intrinsics map[*ast.CallExpr]*IntrinsicCall
	// Devices holds the pin every dev expression resolved to. A reference to a
	// dev parameter is absent: which device it names is a property of the call
	// site, and the call sites are what IR generation splices the body into.
	Devices map[ast.Expr]Device
}

// Analyze checks file and returns the typed program and every problem it found,
// ordered by source position. An empty diagnostic list means the program is
// well formed.
//
// The returned error is not a rejection of the program: it reports that
// analysis could not run, because an argument was nil or ctx was cancelled.
// Source problems are diagnostics.
//
// file is expected to have parsed without diagnostics. Constructs the parser
// already rejected are not reported a second time.
func Analyze(ctx context.Context, file *ast.File, tables Tables) (*Program, source.DiagnosticList, error) {
	if file == nil {
		return nil, nil, errors.New("sema: nil file")
	}
	if tables == nil {
		return nil, nil, errors.New("sema: nil machine tables")
	}
	c, err := newChecker(file, tables)
	if err != nil {
		return nil, nil, err
	}
	if err := c.run(ctx); err != nil {
		return nil, nil, err
	}
	c.diags.Sort()
	return c.prog, c.diags, nil
}

type checker struct {
	tables Tables
	prog   *Program
	diags  source.DiagnosticList

	// reported suppresses an identical message at an identical position, which
	// is how a construct examined twice would otherwise be reported twice.
	reported  map[string]bool
	truncated bool

	scope    *scope
	fn       *Func
	funcs    map[string]*Func
	loops    int
	switches int
}

func newChecker(file *ast.File, tables Tables) (*checker, error) {
	universe, err := universe()
	if err != nil {
		return nil, fmt.Errorf("sema: building the scope enclosing file scope: %w", err)
	}
	return &checker{
		tables: tables,
		prog: &Program{
			File:        file,
			Types:       make(map[ast.Expr]*Type),
			Uses:        make(map[*ast.Ident]*Symbol),
			Consts:      make(map[ast.Expr]Value),
			Conversions: make(map[ast.Expr]*Type),
			Calls:       make(map[*ast.CallExpr]*Func),
			Intrinsics:  make(map[*ast.CallExpr]*IntrinsicCall),
			Devices:     make(map[ast.Expr]Device),
		},
		reported: make(map[string]bool),
		scope:    newScope(universe),
		funcs:    make(map[string]*Func),
	}, nil
}

func (c *checker) run(ctx context.Context) error {
	for _, d := range c.prog.File.Decls {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("analyzing %s: %w", c.prog.File.Name, err)
		}
		c.decl(d)
	}
	c.checkEntryPoint()
	c.checkDefined()
	c.checkDefiniteAssignment()
	markRecursive(c.prog.Funcs)
	c.checkDeviceParams()
	return nil
}

// checkDeviceParams reports a dev parameter on a function that cannot be
// inlined.
//
// A device operand is resolved when the chip assembles the line, so it has to
// be a literal there; a parameter is one only because every call site's body is
// spliced in, which substitutes the argument the site wrote. A recursive
// function has no such substitution — a body cannot be spliced into itself — so
// its parameter would have to become a register the machine will not read as a
// device.
//
// It runs after markRecursive, which is what decides the same question IR
// generation later asks.
func (c *checker) checkDeviceParams() {
	for _, fn := range c.prog.Funcs {
		if !fn.Recursive {
			continue
		}
		for _, param := range fn.Params {
			if unqual(param.Type).Kind() != Dev {
				continue
			}
			c.errorf(param.Pos, "'%s' takes the dev parameter '%s' and can reach itself through a call, so it is compiled out of line rather than inlined; the chip resolves a device position when the line is assembled and a real call would have to pass the pin in a register, which is not a spelling it reads — name the device at each use, or rewrite the recursion as a loop", fn.Name, param.Name)
		}
	}
}

// checkDefined reports a function a call reached but no definition supplies.
// There is no linker: every function called lives in this file or nowhere. The
// entry point is exempt because checkEntryPoint requires its body whether or
// not anything calls it.
func (c *checker) checkDefined() {
	for _, fn := range c.prog.Funcs {
		if fn == c.prog.Main {
			continue
		}
		if fn.Decl == nil && fn.called {
			c.errorf(fn.Pos, "'%s' is called but never defined; MicroC has no linker", fn.Name)
		}
	}
}

func (c *checker) errorf(pos source.Position, format string, args ...any) {
	if msg, ok := c.record(pos, format, args...); ok {
		c.diags.Addf(pos, "%s", msg)
	}
}

// warnf records a diagnostic that does not reject the program. An operand
// checked twice reaches here twice, so it shares errorf's suppression of an
// identical message at an identical position.
func (c *checker) warnf(pos source.Position, format string, args ...any) {
	if msg, ok := c.record(pos, format, args...); ok {
		c.diags.Warnf(pos, "%s", msg)
	}
}

// record applies the two limits every diagnostic is subject to: one message per
// position, and a cap past which the output is noise. It reports whether the
// message should be added.
func (c *checker) record(pos source.Position, format string, args ...any) (string, bool) {
	msg := fmt.Sprintf(format, args...)
	key := pos.String() + ": " + msg
	if c.reported[key] {
		return "", false
	}
	c.reported[key] = true
	if len(c.diags) >= maxDiagnostics {
		if !c.truncated {
			c.truncated = true
			c.diags.Addf(pos, "too many errors")
		}
		return "", false
	}
	return msg, true
}

func (c *checker) push() { c.scope = newScope(c.scope) }

func (c *checker) pop() { c.scope = c.scope.parent }

// declare adds sym to the current scope, reporting a name already declared
// there. The first declaration keeps the name, so later uses resolve to
// something rather than cascading into "undeclared".
func (c *checker) declare(sym *Symbol) {
	if prev := c.scope.lookupLocal(sym.Name); prev != nil {
		c.errorf(sym.Pos, "'%s' is already declared at %s", sym.Name, prev.Pos)
		return
	}
	c.scope.insert(sym)
}

// preludeTypeNames are the type names the generated C header introduces, less
// dev, which is a MicroC keyword and which the parser therefore refuses first.
//
// They are reserved for the other direction of the subset claim. A MicroC
// program is read as C through that header, where a declaration taking one of
// these spellings redefines a typedef and is a hard error — so accepting it
// here would admit a program no C toolchain translates.
//
// The generator writes the header and cannot be imported from here. What keeps
// the two in step is TestPreludeTypeNamesAreNotDeclarable, which reads the
// names back out of the generated text and requires each one to be refused.
var preludeTypeNames = []string{"ic10_logic", "ic10_slot", "ic10_batch", "ic10_reagent"}

func isPreludeTypeName(name string) bool { return slices.Contains(preludeTypeNames, name) }

// checkReserved rejects a declaration of a name the language keeps for itself:
// the intrinsic prefix, the device pin spellings, the machine names an
// intrinsic argument carries, the machine's own constants, and the type names
// the C prelude introduces.
//
// The machine spellings are reserved because nothing consults scope where they
// mean something. A device position and a named intrinsic operand resolve their
// argument from the tables alone, so a variable of the same name is not the
// thing the argument denotes and a program declaring one would read as though
// it were.
func (c *checker) checkReserved(pos source.Position, name string) {
	switch {
	case isReservedName(name):
		c.errorf(pos, "a name beginning '%s' is reserved for intrinsics", reservedPrefix)
	case isDevicePinSpelling(name):
		c.errorf(pos, "'%s' names a device pin; the spelling is reserved, since a device position resolves it without consulting scope", name)
	case isMachineConstant(name):
		c.errorf(pos, "'%s' is one of the machine's own constants, predeclared as a constexpr double; the spelling is reserved", name)
	case isPreludeTypeName(name):
		c.errorf(pos, "'%s' is the name the C prelude gives one of the operand types; the spelling is reserved, since a C editor reading this program as C would see a redefinition", name)
	default:
		if kind, taken := c.machineName(name); taken {
			c.errorf(pos, "'%s' is a %s; the spelling is reserved, since an intrinsic argument resolves a machine name without consulting scope", name, kind)
		}
	}
}

// machineName reports which family of machine names holds name, and false where
// none does, in which case the kind means nothing. It asks the four table
// queries rather than a fifth of its own, so what a declaration may not spell is
// exactly what an intrinsic argument resolves.
func (c *checker) machineName(name string) (OperandKind, bool) {
	if _, ok := c.tables.LogicType(name); ok {
		return OperandLogicType, true
	}
	if _, ok := c.tables.LogicSlotType(name); ok {
		return OperandSlotType, true
	}
	if _, ok := c.tables.BatchMode(name); ok {
		return OperandBatchMode, true
	}
	if _, ok := c.tables.ReagentMode(name); ok {
		return OperandReagentMode, true
	}
	return OperandValue, false
}

// EntryFunction is the name of the function execution begins in. The back end
// emits it first so that its first instruction is line 0, which is where the
// chip starts, so the two stages have to agree on the name.
const EntryFunction = "main"

// checkEntryPoint reports the program's entry point, which every other function
// is reachable from. A program defines main: a prototype alone leaves execution
// nowhere to begin, and no call has to reach main for that to be a mistake.
func (c *checker) checkEntryPoint() {
	fn := c.funcs[EntryFunction]
	if fn == nil {
		c.errorf(c.prog.File.Start, "the program does not define 'void main(void)', where execution begins")
		return
	}
	c.prog.Main = fn
	if fn.Result.Kind() != Void || len(fn.Params) != 0 {
		c.errorf(fn.Pos, "'main' must be declared 'void main(void)'")
	}
	if fn.Decl == nil {
		c.errorf(fn.Pos, "'main' is declared but never defined; execution begins in its body")
	}
}
