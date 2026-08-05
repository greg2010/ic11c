package irgen

import (
	"testing"

	"github.com/greg2010/ic11c/internal/valueflow"
	"tinygo.org/x/go-llvm"
)

// zeroStores counts, per local, the stores of zero the module writes into it,
// resolving each store's pointer back to the object it addresses. An unplaceable
// pointer fails the case rather than being passed over, since
// [valueflow.MemoryObject] answers nil for every object at once.
func zeroStores(t *testing.T, m llvm.Module) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IsDeclaration() {
			continue
		}
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for in := bb.FirstInstruction(); !in.IsNil(); in = llvm.NextInstruction(in) {
				if in.InstructionOpcode() != llvm.Store || !in.Operand(0).IsNull() {
					continue
				}
				base := valueflow.MemoryObject(in.Operand(1))
				if base.IsNil() {
					t.Fatalf("a store of zero in %q addresses a pointer the walk cannot place, which stands for every object:\n%s", fn.Name(), m.String())
				}
				counts[base.Name()]++
			}
		}
	}
	return counts
}

// TestZeroesTheDataRegionALocalDeclarationLeavesUnwritten covers the promise
// that a data-region object reads as zero before it is first written. The entry
// prologue zeroes all 512 slots in one instruction, but the IR states it anyway:
// an alloca LLVM sees no store into is undef, and the optimizer folds through one.
func TestZeroesTheDataRegionALocalDeclarationLeavesUnwritten(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// want counts the zero stores expected against each named local.
		want map[string]int
	}{
		{
			name: "an address-taken local with no initializer",
			src: `void main(void) {
    long long x;
    long long *p = &x;
    if (*p == 0) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}`,
			want: map[string]int{"x": 1},
		},
		{
			name: "an array of the entry point states its zero in its initializer",
			src: `void main(void) {
    long long a[4];
    a[0] = 1;
    if (a[2] == 0) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}`,
			want: map[string]int{},
		},
		{
			name: "the elements a brace initializer did not supply cost nothing either",
			src: `void main(void) {
    long long a[4] = {1, 2};
    if (a[3] == 0) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}`,
			want: map[string]int{},
		},
		{
			name: "an array a brace initializer filled",
			src: `void main(void) {
    long long a[2] = {1, 2};
    __ic_store(d1, On, a[0] + a[1]);
}`,
			want: map[string]int{},
		},
		{
			name: "an address-taken local its declaration initialized",
			src: `void main(void) {
    long long x = 3;
    long long *p = &x;
    __ic_store(d1, On, *p);
}`,
			want: map[string]int{},
		},
		{
			// Nothing zeroes a register, and definite assignment refuses a read
			// that no write reaches.
			name: "a local the optimizer keeps in a register",
			src: `void main(void) {
    long long n;
    n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, On, n);
}`,
			want: map[string]int{},
		},
		{
			name: "an array local to a function compiled out of line",
			src: `long long step(long long k) {
    long long a[2];
    a[0] = k;
    if (k <= 0) { return a[1]; }
    return step(k - 1);
}
void main(void) {
    __ic_store(d1, On, step(3));
}`,
			want: map[string]int{"a": 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := lower(t, tc.src)
			got := zeroStores(t, m)
			for name, count := range tc.want {
				if got[name] != count {
					t.Errorf("%q takes %d stores of zero, want %d\n%s", name, got[name], count, m.String())
				}
			}
			for name, count := range got {
				if _, expected := tc.want[name]; !expected {
					t.Errorf("%q takes %d stores of zero, want none\n%s", name, count, m.String())
				}
			}
		})
	}
}

// TestZeroingIsHoistedAboveTheLoopThatDeclares covers where the stores land. A
// data-region local is zeroed once, before anything else runs, so one re-entered
// by a loop holds what the last iteration wrote. Emitting the stores at the
// declaration would say the opposite and cost an instruction per element per pass.
func TestZeroingIsHoistedAboveTheLoopThatDeclares(t *testing.T) {
	const src = `long long step(long long k) {
    for (long long i = 0; i < 4; i++) {
        long long a[2];
        __ic_store(d1, On, a[1]);
        a[1] = i + k;
    }
    if (k <= 0) { return 0; }
    return step(k - 1);
}
void main(void) { __ic_store(d2, On, step(3)); }`
	m := lower(t, src)

	fn := m.NamedFunction("step")
	if fn.IsNil() {
		t.Fatalf("the module defines no step:\n%s", m.String())
	}
	entry := fn.EntryBasicBlock()
	found := 0
	for in := entry.FirstInstruction(); !in.IsNil(); in = llvm.NextInstruction(in) {
		if in.InstructionOpcode() != llvm.Store || !in.Operand(0).IsNull() {
			continue
		}
		base := valueflow.MemoryObject(in.Operand(1))
		if base.IsNil() {
			t.Fatalf("a store of zero in the entry block of 'step' addresses a pointer the walk cannot place:\n%s", m.String())
		}
		if base.Name() == "a" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("the entry block zeroes %d of the 2 elements of 'a'\n%s", found, m.String())
	}
}
