package difftest

import "testing"

// TestSafeBlocksEmitOneLine holds the padding emitters to the property
// emitRelativeBranch computes its offset from: an emitter that grew a second
// line would leave the offset landing mid-block rather than past it, while
// the program still assembled and terminated.
func TestSafeBlocksEmitOneLine(t *testing.T) {
	for seed := range uint64(256) {
		for i, emit := range safeBlocks {
			g := newGenerator(seed)
			emit(g)
			if len(g.lines) != 1 || len(g.pending) != 0 {
				t.Fatalf("seed %d: safeBlocks[%d] emitted %d lines and reserved %d labels, want 1 and 0: %q",
					seed, i, len(g.lines), len(g.pending), g.lines)
			}
		}
	}
}
