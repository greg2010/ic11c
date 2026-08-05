package sema_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/tsparse"
)

func TestAnalyzeBuildsCallGraph(t *testing.T) {
	const src = `constexpr long long kStart = 2;
long long fact(long long n) {
    return n <= 1 ? 1 : n * fact(n - 1);
}
long long down(long long n);
long long up(long long n) {
    return n == 0 ? 0 : down(n - 1);
}
long long down(long long n) {
    return n == 0 ? 0 : up(n - 1);
}
long long leaf(void) {
    return 1;
}
void main(void) {
    __ic_sleep(fact(kStart) + up(2) + leaf());
}
`
	prog, diags := analyze(t, src)
	if len(diags) != 0 {
		t.Fatalf("analysis rejected a valid program:\n%s", diags.String())
	}

	byName := make(map[string]*sema.Func, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	wantRecursive := map[string]bool{
		"fact": true,
		"up":   true,
		"down": true,
		"leaf": false,
		"main": false,
	}
	for name, want := range wantRecursive {
		fn, ok := byName[name]
		if !ok {
			t.Fatalf("no function %q in the program", name)
		}
		if fn.Recursive != want {
			t.Errorf("%s.Recursive = %v, want %v", name, fn.Recursive, want)
		}
	}

	if prog.Main == nil || prog.Main != byName["main"] {
		t.Fatalf("Main = %v, want the main function", prog.Main)
	}
	var callees []string
	for _, callee := range prog.Main.Callees {
		callees = append(callees, callee.Name)
	}
	want := []string{"fact", "up", "leaf"}
	if len(callees) != len(want) {
		t.Fatalf("main calls %v, want %v", callees, want)
	}
	for i, name := range want {
		if callees[i] != name {
			t.Errorf("main callee %d is %q, want %q", i, callees[i], name)
		}
	}

	if len(prog.Globals) != 1 || prog.Globals[0].Name != "kStart" {
		t.Fatalf("Globals = %v, want only kStart", prog.Globals)
	}
	if v := prog.Globals[0].Value; v == nil || v.Int != 2 {
		t.Errorf("kStart folded to %v, want 2", v)
	}
}

func TestAnalyzeResolvesIntrinsicOperands(t *testing.T) {
	const src = `void main(void) {
    __ic_store_slot(d3, 2, Occupied, __ic_hash("StructureStubFurnace"));
    __ic_store_batch(__ic_hash("StructureStubLight"), On, __ic_load(db, Pressure));
}
`
	prog, diags := analyze(t, src)
	if len(diags) != 0 {
		t.Fatalf("analysis rejected a valid program:\n%s", diags.String())
	}

	// Every intrinsic call the source writes, in source order. The two
	// __ic_hash calls resolve to different values, so the assertion has to name
	// call sites rather than intrinsics.
	tests := []struct {
		intrinsic string
		args      []sema.Operand
	}{
		{
			intrinsic: "__ic_store_slot",
			args: []sema.Operand{
				{Kind: sema.OperandDevice, Name: "d3", Value: 3, Resolved: true},
				{Kind: sema.OperandSlot, Value: 2, Resolved: true},
				{Kind: sema.OperandSlotType, Name: "Occupied", Value: 0, Resolved: true},
				{Kind: sema.OperandDouble},
			},
		},
		{
			// The hash of a prefab name is its CRC-32 read as a signed 32-bit
			// integer, which is how the game publishes it.
			intrinsic: "__ic_hash",
			args:      []sema.Operand{{Kind: sema.OperandString, Name: "StructureStubFurnace", Value: 146307392, Resolved: true}},
		},
		{
			intrinsic: "__ic_store_batch",
			args: []sema.Operand{
				{Kind: sema.OperandValue},
				{Kind: sema.OperandLogicType, Name: "On", Value: 0, Resolved: true},
				{Kind: sema.OperandDouble},
			},
		},
		{
			intrinsic: "__ic_hash",
			args:      []sema.Operand{{Kind: sema.OperandString, Name: "StructureStubLight", Value: 819625054, Resolved: true}},
		},
		{
			intrinsic: "__ic_load",
			args: []sema.Operand{
				{Kind: sema.OperandDevice, Name: "db", Value: -1, Resolved: true},
				{Kind: sema.OperandLogicType, Name: "Pressure", Value: 5, Resolved: true},
			},
		},
	}

	sites := intrinsicSites(t, prog)
	if len(sites) != len(tests) {
		t.Fatalf("recorded %d intrinsic calls, want %d", len(sites), len(tests))
	}
	for i, tt := range tests {
		call := prog.Intrinsics[sites[i]]
		if call.Intrinsic.Name != tt.intrinsic {
			t.Errorf("call %d at %s is %s, want %s", i, sites[i].Pos(), call.Intrinsic.Name, tt.intrinsic)
			continue
		}
		if len(call.Args) != len(tt.args) {
			t.Errorf("call %d to %s recorded %d operands, want %d", i, tt.intrinsic, len(call.Args), len(tt.args))
			continue
		}
		for j, want := range tt.args {
			if got := call.Args[j]; got != want {
				t.Errorf("call %d to %s operand %d = %+v, want %+v", i, tt.intrinsic, j, got, want)
			}
		}
	}
}

// intrinsicSites returns every recorded intrinsic call site in source order.
// Program.Intrinsics is a map, so iterating it directly decides nothing about
// which of two calls to the same intrinsic a test looks at.
func intrinsicSites(t *testing.T, prog *sema.Program) []*ast.CallExpr {
	t.Helper()
	sites := make([]*ast.CallExpr, 0, len(prog.Intrinsics))
	for call := range prog.Intrinsics {
		sites = append(sites, call)
	}
	slices.SortFunc(sites, func(a, b *ast.CallExpr) int {
		return cmp.Compare(a.Pos().Offset, b.Pos().Offset)
	})
	return sites
}

func TestAnalyzeRecordsConversionsAndUses(t *testing.T) {
	const src = `bool flag;
void main(void) {
    long long x = flag;
    flag = x;
    if (x) {
        x = 0;
    }
}
`
	prog, diags := analyze(t, src)
	if len(diags) != 0 {
		t.Fatalf("analysis rejected a valid program:\n%s", diags.String())
	}

	// The initializer converts bool to int, the assignment converts back, and
	// the condition normalizes an int to bool.
	counts := map[sema.Kind]int{}
	for _, target := range prog.Conversions {
		counts[target.Kind()]++
	}
	if counts[sema.Int] != 1 || counts[sema.Bool] != 2 {
		t.Errorf("recorded %d conversions to long long and %d to bool, want 1 and 2", counts[sema.Int], counts[sema.Bool])
	}

	used := make(map[string]*sema.Symbol)
	for _, sym := range prog.Uses {
		used[sym.Name] = sym
	}
	for name, want := range map[string]sema.SymbolKind{"flag": sema.GlobalVar, "x": sema.LocalVar} {
		sym, ok := used[name]
		if !ok {
			t.Fatalf("no use of %q was recorded", name)
		}
		if sym.Kind != want {
			t.Errorf("%q resolved to a %s, want a %s", name, sym.Kind, want)
		}
	}
	if len(prog.Types) == 0 {
		t.Error("no expression types were recorded")
	}
}

// TestAnalyzeToleratesBadNodes runs analysis over a tree the parser filled with
// Bad nodes. A driver may reach analysis anyway, and a checker that walks off a
// node the parser could not build is a crash rather than a diagnostic.
func TestAnalyzeToleratesBadNodes(t *testing.T) {
	const src = `long long x = ;
void f( {
}
void main(void) {
    x = ;
    switch (x) {
    case :
    }
}
`
	file, parseDiags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(parseDiags) == 0 {
		t.Fatal("the source was expected to fail parsing")
	}
	if _, _, err := sema.Analyze(context.Background(), file, testTables{}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
}

func TestAnalyzeRejectsMisuse(t *testing.T) {
	file, diags, err := tsparse.Parse("test.c", "void main(void) {}\n")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("source did not parse cleanly:\n%s", diags.String())
	}

	if _, _, err := sema.Analyze(context.Background(), nil, testTables{}); err == nil {
		t.Error("Analyze accepted a nil file")
	}
	if _, _, err := sema.Analyze(context.Background(), file, nil); err == nil {
		t.Error("Analyze accepted nil machine tables")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = sema.Analyze(ctx, file, testTables{})
	if err == nil {
		t.Fatal("Analyze ignored a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Analyze returned %v, want it to wrap context.Canceled", err)
	}
}
