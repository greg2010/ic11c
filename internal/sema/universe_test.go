package sema_test

import (
	"context"
	"sync"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// takesConstantAddress applies '&' to a machine constant, which is the
// construct that reached the one write analysis ever made to a symbol the scope
// enclosing file scope binds. It is rejected, and the tests below run it for
// what it reaches on the way rather than for its result.
const takesConstantAddress = `void main(void) {
    const double *p = &pi;
    __ic_store(d0, Setting, *p);
}
`

// readsConstant names the same constant without addressing it, so every fact
// analysis records about the symbol here is one this program stated.
const readsConstant = `void main(void) {
    __ic_store(d0, Setting, pi);
}
`

// TestUniverseSymbolsAreNotSharedBetweenAnalyses states the invariant the race
// and the leak are both instances of: no [sema.Symbol] one analysis can reach
// is reachable from another. Analysis writes to a symbol as it goes, so one
// shared between analyses carries a fact about one program into the next and
// races when the two run at once.
//
// It compares identity rather than contents because identity is the property:
// two analyses whose symbols happen to agree today are still one write apart
// from disagreeing.
func TestUniverseSymbolsAreNotSharedBetweenAnalyses(t *testing.T) {
	first, _ := analyze(t, readsConstant)
	second, _ := analyze(t, readsConstant)

	reached := make(map[*sema.Symbol]string, len(first.Uses))
	for _, sym := range first.Uses {
		reached[sym] = sym.Name
	}
	for _, sym := range second.Uses {
		if name, shared := reached[sym]; shared {
			t.Errorf("both analyses reach the same symbol for '%s'", name)
		}
	}
}

// TestAddressingAConstantLeavesNoMarkOnTheNextAnalysis is the leak: a fact one
// program stated is read by the next one, which never stated it.
func TestAddressingAConstantLeavesNoMarkOnTheNextAnalysis(t *testing.T) {
	// The first analysis has to reach the constant for it to have had anything
	// to leave behind, and a program that stopped naming it would otherwise
	// leave the second one with nothing to prove.
	addressing, _ := analyze(t, takesConstantAddress)
	if symbolNamed(addressing, "pi") == nil {
		t.Fatal("the program taking an address resolved no use of 'pi'")
	}

	prog, diags := analyze(t, readsConstant)
	if diags.HasErrors() {
		t.Fatalf("the program that only reads 'pi' was rejected:\n%s", diags.String())
	}
	sym := symbolNamed(prog, "pi")
	if sym == nil {
		t.Fatal("no use of 'pi' resolved")
	}
	if sym.Addressed {
		t.Errorf("'pi' is marked addressed in a program that never took its address")
	}
}

// TestConcurrentAnalysesDoNotRaceOnTheUniverse is the race, and is meaningful
// under -race alone: two analyses writing one symbol is a data race whichever
// order they land in.
//
// Every file is parsed before the goroutines start, so the analyses share
// nothing but what the package itself holds, and the barrier is what makes them
// overlap rather than run one after another.
func TestConcurrentAnalysesDoNotRaceOnTheUniverse(t *testing.T) {
	const analyses = 8

	files := make([]*ast.File, analyses)
	for i := range files {
		file, diags, err := tsparse.Parse("test.c", takesConstantAddress)
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		if len(diags) != 0 {
			t.Fatalf("source did not parse cleanly:\n%s", diags.String())
		}
		files[i] = file
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, file := range files {
		wg.Go(func() {
			<-start
			prog, _, err := sema.Analyze(context.Background(), file, testTables{})
			if err != nil {
				t.Errorf("Analyze: %v", err)
				return
			}
			// The analysis has to reach the constant for the race to be the
			// one under test, and a program that stopped naming it would
			// otherwise pass here having exercised nothing.
			if symbolNamed(prog, "pi") == nil {
				t.Errorf("the analysis resolved no use of 'pi'")
			}
		})
	}
	close(start)
	wg.Wait()
}

// symbolNamed finds the symbol a resolved use of name denotes, and nil where
// nothing in the program resolved to that name.
func symbolNamed(prog *sema.Program, name string) *sema.Symbol {
	for _, sym := range prog.Uses {
		if sym.Name == name {
			return sym
		}
	}
	return nil
}
