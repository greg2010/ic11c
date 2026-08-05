package regalloc

import (
	"reflect"
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/mir"
)

// TestByStartIsATotalOrder is what keeps the emitted program a function of its
// input. buildIntervals collects intervals in map order and hands them to an
// unstable sort, so a pair byStart returned zero for would let map traversal
// decide every register, every spill victim and every spill slot index.
func TestByStartIsATotalOrder(t *testing.T) {
	if got := reflect.TypeFor[mir.VirtReg]().NumField(); got != 1 {
		t.Errorf("mir.VirtReg has %d fields and byStart separates on ID alone; give byStart the new field or intervals will tie", got)
	}

	at := func(start int, id uint32) *interval {
		return &interval{vreg: mir.VirtReg{ID: id}, ranges: []liveRange{{from: start, to: start + 2}}}
	}
	tests := []struct {
		name string
		a, b *interval
		want int
	}{
		{name: "an earlier start comes first", a: at(2, 9), b: at(6, 0), want: -1},
		{name: "a later start comes second", a: at(6, 0), b: at(2, 9), want: 1},
		{name: "a tied start falls to the lower id", a: at(4, 1), b: at(4, 2), want: -1},
		{name: "a tied start is antisymmetric", a: at(4, 2), b: at(4, 1), want: 1},
		{name: "an interval orders with itself", a: at(4, 1), b: at(4, 1), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := byStart(tt.a, tt.b); sign(got) != tt.want {
				t.Errorf("byStart = %d, want sign %d", got, tt.want)
			}
		})
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestIntervalAddRange(t *testing.T) {
	tests := []struct {
		name string
		// adds is applied in order, which mirrors the backward walk: from never
		// exceeds the start of the first range already recorded.
		adds []liveRange
		want []liveRange
	}{
		{name: "single range", adds: []liveRange{{4, 8}}, want: []liveRange{{4, 8}}},
		{name: "empty range is dropped", adds: []liveRange{{4, 4}}, want: nil},
		{name: "overlapping merges", adds: []liveRange{{4, 8}, {2, 6}}, want: []liveRange{{2, 8}}},
		{name: "adjacent merges", adds: []liveRange{{4, 8}, {1, 4}}, want: []liveRange{{1, 8}}},
		{name: "separated keeps the hole", adds: []liveRange{{6, 8}, {1, 3}}, want: []liveRange{{1, 3}, {6, 8}}},
		{name: "three ranges", adds: []liveRange{{10, 12}, {6, 7}, {1, 3}}, want: []liveRange{{1, 3}, {6, 7}, {10, 12}}},
		{name: "enclosed range is absorbed", adds: []liveRange{{2, 12}, {4, 6}}, want: []liveRange{{2, 12}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var iv interval
			for _, r := range tt.adds {
				iv.addRange(r.from, r.to)
			}
			if !slices.Equal(iv.ranges, tt.want) {
				t.Errorf("ranges = %v, want %v", iv.ranges, tt.want)
			}
		})
	}
}

func TestIntervalSetFrom(t *testing.T) {
	tests := []struct {
		name  string
		adds  []liveRange
		point int
		want  []liveRange
	}{
		{name: "shortens the first range", adds: []liveRange{{2, 10}}, point: 5, want: []liveRange{{5, 10}}},
		{name: "later ranges are untouched", adds: []liveRange{{8, 10}, {2, 4}}, point: 3, want: []liveRange{{3, 4}, {8, 10}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var iv interval
			for _, r := range tt.adds {
				iv.addRange(r.from, r.to)
			}
			iv.setFrom(tt.point)
			if !slices.Equal(iv.ranges, tt.want) {
				t.Errorf("ranges = %v, want %v", iv.ranges, tt.want)
			}
		})
	}
}

func TestIntervalQueries(t *testing.T) {
	holed := &interval{ranges: []liveRange{{2, 6}, {12, 16}}}
	solid := &interval{ranges: []liveRange{{2, 16}}}

	t.Run("span excludes holes", func(t *testing.T) {
		if got, want := holed.span(), 8; got != want {
			t.Errorf("span = %d, want %d", got, want)
		}
		if got, want := solid.span(), 14; got != want {
			t.Errorf("span = %d, want %d", got, want)
		}
	})

	t.Run("bounds", func(t *testing.T) {
		if got, want := holed.start(), 2; got != want {
			t.Errorf("start = %d, want %d", got, want)
		}
		if got, want := holed.end(), 16; got != want {
			t.Errorf("end = %d, want %d", got, want)
		}
	})

	coverTests := []struct {
		point int
		want  bool
	}{
		{point: 1, want: false},
		{point: 2, want: true},
		{point: 5, want: true},
		{point: 6, want: false},
		{point: 11, want: false},
		{point: 12, want: true},
		{point: 16, want: false},
		{point: 99, want: false},
	}
	for _, tt := range coverTests {
		if got := holed.covers(tt.point); got != tt.want {
			t.Errorf("covers(%d) = %v, want %v", tt.point, got, tt.want)
		}
	}
}

// TestIntervalIntersects is what keeps a hole worth having: an interval that
// fits entirely inside another's hole does not interfere with it, so the two
// share a register.
func TestIntervalIntersects(t *testing.T) {
	tests := []struct {
		name string
		a    []liveRange
		b    []liveRange
		want bool
	}{
		{name: "disjoint", a: []liveRange{{0, 4}}, b: []liveRange{{4, 8}}, want: false},
		{name: "overlapping", a: []liveRange{{0, 5}}, b: []liveRange{{4, 8}}, want: true},
		{name: "inside a hole", a: []liveRange{{0, 4}, {12, 16}}, b: []liveRange{{6, 10}}, want: false},
		{name: "straddling a hole", a: []liveRange{{0, 4}, {12, 16}}, b: []liveRange{{3, 13}}, want: true},
		{name: "second range only", a: []liveRange{{0, 4}, {12, 16}}, b: []liveRange{{14, 20}}, want: true},
		{name: "holes interleave", a: []liveRange{{0, 2}, {4, 6}}, b: []liveRange{{2, 4}, {6, 8}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &interval{ranges: tt.a}
			b := &interval{ranges: tt.b}
			if got := a.intersects(b); got != tt.want {
				t.Errorf("a.intersects(b) = %v, want %v", got, tt.want)
			}
			if got := b.intersects(a); got != tt.want {
				t.Errorf("b.intersects(a) = %v, want %v", got, tt.want)
			}
		})
	}
}
