// Package sema type-checks a parsed MicroC translation unit into a
// [Program]. A node that fails to type takes the Invalid type, which
// absorbs further operations silently, so analysis never stops at the first
// error. The pointer restriction is checked again after optimization.
package sema

import (
	"context"
	"errors"
	"fmt"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// Program is a checked translation unit, with every map keyed by the syntax
// node the parser produced. When the returned diagnostics hold an error the
// program is partial: unresolved names are absent from Uses and expressions
// that failed to type are Invalid in Types.
type Program struct {
	File *ast.File
	// Globals holds every file-scope variable in declaration order.
	Globals []*Symbol
	// Funcs holds every function in declaration order, each carrying its call
	// edges and whether it is recursive.
	Funcs []*Func
	// Main is the entry point, or nil if the program declares no main. It is
	// set as soon as the name resolves, before signature and body checks run,
	// so a non-nil Main may carry a rejected signature. Only a clean
	// diagnostic list makes it usable.
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
	// dev parameter is absent, since which device it names depends on the
	// call site.
	Devices map[ast.Expr]Device
}

// Analyze checks file and returns the typed program and every diagnostic
// found, ordered by source position; an empty list means the program is well
// formed. The returned error means analysis itself could not run — a nil
// argument or a cancelled ctx — never that the program was rejected.
func Analyze(ctx context.Context, file *ast.File, tables Tables) (*Program, source.DiagnosticList, error) {
	return analyze(ctx, file, tables, ast.MaxNestingDepth)
}

// analyze is [Analyze] with the nesting bound as a parameter, so tests can
// probe the limit without building a tree deep enough to reach the real one.
func analyze(ctx context.Context, file *ast.File, tables Tables, maxDepth int) (*Program, source.DiagnosticList, error) {
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
	if pos, deep := ast.DeeperThan(file, maxDepth); deep {
		// Refused whole: this is the only diagnostic returned, and prog is
		// otherwise empty.
		c.errorf(pos, "nested too deeply; the compiler reads at most %d constructs inside one another, "+
			"and every pass behind the parser walks this tree with a recursion of its own", maxDepth)
		return c.prog, c.diags, nil
	}
	if err := c.run(ctx); err != nil {
		return nil, nil, err
	}
	c.diags.Sort()
	c.closeLists()
	return c.prog, c.diags, nil
}

type checker struct {
	tables Tables
	prog   *Program
	diags  source.DiagnosticList

	// reported suppresses an identical message at an identical position, which
	// is how a construct examined twice would otherwise be reported twice.
	reported map[string]bool
	errors   budget
	warnings budget

	folded     map[foldKey]foldResult
	ctypes     map[ast.Expr]ctypeResult
	terminated map[ast.Stmt]bool
	slots      map[ast.Expr]slotResult

	scope    *scopes
	fn       *Func
	funcs    map[string]*Func
	loops    int
	switches int
	// addressed is the lvalue '&' is currently being applied to. A subscript
	// there denotes an address rather than an object, which is what lets
	// [checker.checkIndexBound] admit the one-past-the-end pointer C defines.
	addressed ast.Expr
	// dereferenced is the operand of the '*' currently being applied. A '&'
	// there is one the '*' cancels: '*&E' reads E, so [checker.addrOf] leaves
	// no address under it for that same rule to admit.
	dereferenced ast.Expr

	// accesses holds every batch and pin instruction, checked against the prefab
	// roster once the file has been read. See [deviceCall].
	accesses []deviceCall
	// pins holds what a declaration promised is wired to each housing position,
	// which is what makes a pin-addressed access checkable. See [pinClaim].
	pins map[Device]pinClaim

	// intType is the integer type spelled the way this file spells it, and names
	// that type wherever no written type produced one.
	intType *Type
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
		reported:   make(map[string]bool),
		errors:     budget{max: maxErrors},
		warnings:   budget{max: maxWarnings},
		folded:     make(map[foldKey]foldResult),
		ctypes:     make(map[ast.Expr]ctypeResult),
		terminated: make(map[ast.Stmt]bool),
		slots:      make(map[ast.Expr]slotResult),
		scope:      newScopes(universe),
		funcs:      make(map[string]*Func),
		pins:       make(map[Device]pinClaim),
		intType:    spelled(IntType, file.IntSpelling),
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
	c.checkDeviceSurfaces()
	markRecursive(c.prog.Funcs)
	c.checkDeviceParams()
	return nil
}

// EntryFunction is the name of the function execution begins in. The chip
// starts at instruction line 0, so the back end must emit this function first.
const EntryFunction = "main"
