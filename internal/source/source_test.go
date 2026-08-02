package source_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

func TestPositionString(t *testing.T) {
	tests := []struct {
		name string
		pos  source.Position
		want string
	}{
		{name: "with file", pos: source.Position{File: "a.c", Line: 3, Column: 7}, want: "a.c:3:7"},
		{name: "without file", pos: source.Position{Line: 3, Column: 7}, want: "3:7"},
		{name: "invalid", pos: source.Position{}, want: "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pos.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPositionCompare covers the ordering diagnostics are sorted by. A position
// rebuilt from an LLVM debug location has no offset, so ordering on offsets
// alone would put every backend diagnostic ahead of every front-end one.
func TestPositionCompare(t *testing.T) {
	tests := []struct {
		name string
		a, b source.Position
		want int
	}{
		{
			name: "earlier line first",
			a:    source.Position{Offset: 40, Line: 2, Column: 1},
			b:    source.Position{Line: 9, Column: 1},
			want: -1,
		},
		{
			name: "same line orders by column",
			a:    source.Position{Line: 4, Column: 9},
			b:    source.Position{Line: 4, Column: 2},
			want: 1,
		},
		{
			name: "offset breaks a full tie",
			a:    source.Position{Offset: 3, Line: 4, Column: 2},
			b:    source.Position{Offset: 8, Line: 4, Column: 2},
			want: -1,
		},
		{
			name: "equal",
			a:    source.Position{Offset: 3, Line: 4, Column: 2},
			b:    source.Position{Offset: 3, Line: 4, Column: 2},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Compare(tt.b); got != tt.want {
				t.Errorf("Compare() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLineMapOffset(t *testing.T) {
	const src = "void main(void) {\n    long long x = 1;\n}\n"
	m := source.NewLineMap(src)
	tests := []struct {
		name   string
		line   int
		column int
		want   int
	}{
		{name: "first byte", line: 1, column: 1, want: 0},
		{name: "second line", line: 2, column: 1, want: 18},
		{name: "into the second line", line: 2, column: 5, want: 22},
		{name: "line past the end", line: 99, column: 1, want: 0},
		{name: "line zero", line: 0, column: 1, want: 0},
		{name: "column zero", line: 2, column: 0, want: 0},
		{name: "the newline that ends a line", line: 1, column: 18, want: 17},
		{name: "column past the end of a line", line: 1, column: 19, want: 0},
		{name: "column far past the end of a line", line: 2, column: 400, want: 0},
		{name: "the end of the last line", line: 4, column: 1, want: 41},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.Offset(tt.line, tt.column); got != tt.want {
				t.Errorf("Offset(%d, %d) = %d, want %d", tt.line, tt.column, got, tt.want)
			}
		})
	}
	if got := (*source.LineMap)(nil).Offset(2, 1); got != 0 {
		t.Errorf("nil LineMap Offset() = %d, want 0", got)
	}
}

// TestLineMapPosition covers what every stage running after IR generation
// rebuilds a position with: a debug location carries a line and a column and
// neither the file nor the byte offset.
func TestLineMapPosition(t *testing.T) {
	const src = "void main(void) {\n    long long x = 1;\n}\n"
	tests := []struct {
		name   string
		lines  *source.LineMap
		file   string
		line   int
		column int
		want   source.Position
	}{
		{
			name:  "a position inside the file",
			lines: source.NewLineMap(src), file: "a.c", line: 2, column: 5,
			want: source.Position{File: "a.c", Offset: 22, Line: 2, Column: 5},
		},
		{
			name:  "a position outside the file keeps its line and column",
			lines: source.NewLineMap(src), file: "a.c", line: 99, column: 3,
			want: source.Position{File: "a.c", Offset: 0, Line: 99, Column: 3},
		},
		{
			name:  "no line map leaves the offset at zero",
			lines: nil, file: "a.c", line: 2, column: 5,
			want: source.Position{File: "a.c", Offset: 0, Line: 2, Column: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lines.Position(tt.file, tt.line, tt.column); got != tt.want {
				t.Errorf("Position(%q, %d, %d) = %+v, want %+v", tt.file, tt.line, tt.column, got, tt.want)
			}
		})
	}
}

func TestInlineSiteString(t *testing.T) {
	tests := []struct {
		name string
		site source.InlineSite
		want string
	}{
		{
			name: "named callee at a position",
			site: source.InlineSite{Pos: source.Position{File: "a.c", Line: 8, Column: 3}, Callee: "step"},
			want: "step inlined at a.c:8:3",
		},
		{
			name: "callee the tables did not name",
			site: source.InlineSite{Pos: source.Position{File: "a.c", Line: 8, Column: 3}},
			want: "a call inlined at a.c:8:3",
		},
		{
			name: "position the optimizer merged away",
			site: source.InlineSite{Callee: "step"},
			want: "step inlined at a site the optimizer merged with another",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.site.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPositionLineCol(t *testing.T) {
	pos := source.Position{File: "a.c", Offset: 40, Line: 5, Column: 2}
	want := source.LineCol{Line: 5, Column: 2}
	if got := pos.LineCol(); got != want {
		t.Errorf("LineCol() = %+v, want %+v", got, want)
	}
}

func TestPlural(t *testing.T) {
	tests := []struct {
		name string
		n    int
		noun string
		want string
	}{
		{name: "one keeps the singular", n: 1, noun: "argument", want: "1 argument"},
		{name: "zero takes the plural", n: 0, noun: "argument", want: "0 arguments"},
		{name: "several take the plural", n: 3, noun: "line", want: "3 lines"},
		{name: "a negative count takes the plural", n: -1, noun: "slot", want: "-1 slots"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := source.Plural(tt.n, tt.noun); got != tt.want {
				t.Errorf("Plural(%d, %q) = %q, want %q", tt.n, tt.noun, got, tt.want)
			}
		})
	}
}

func TestDiagnosticList(t *testing.T) {
	var l source.DiagnosticList
	if l.Err() != nil {
		t.Errorf("empty list Err() = %v, want nil", l.Err())
	}

	l.Addf(source.Position{File: "a.c", Offset: 20, Line: 2, Column: 1}, "second %d", 2)
	l.Addf(source.Position{File: "a.c", Offset: 5, Line: 1, Column: 6}, "first")
	l.Sort()

	if l[0].Msg != "first" || l[1].Msg != "second 2" {
		t.Errorf("Sort() left %v", l)
	}
	if got, want := l[0].Error(), "a.c:1:6: first"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := l.Error(), "a.c:1:6: first (and 1 more)"; got != want {
		t.Errorf("list Error() = %q, want %q", got, want)
	}
	if l.Err() == nil {
		t.Error("non-empty list Err() = nil, want an error")
	}
	if got, want := l.String(), "a.c:1:6: first\na.c:2:1: second 2"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestSeveritySeparatesWarningsFromErrors covers what the two severities decide:
// whether a stage stops and whether the command exits non-zero. A list of
// warnings alone is not an error.
func TestSeveritySeparatesWarningsFromErrors(t *testing.T) {
	pos := source.Position{File: "t.c", Line: 3, Column: 1}

	tests := []struct {
		name       string
		build      func() source.DiagnosticList
		wantErrors int
		wantErr    bool
		wantText   string
	}{
		{
			name:  "empty",
			build: func() source.DiagnosticList { return nil },
		},
		{
			name: "one warning",
			build: func() source.DiagnosticList {
				var l source.DiagnosticList
				l.Warnf(pos, "retired")
				return l
			},
			wantText: "t.c:3:1: warning: retired",
		},
		{
			name: "one error",
			build: func() source.DiagnosticList {
				var l source.DiagnosticList
				l.Addf(pos, "broken")
				return l
			},
			wantErrors: 1,
			wantErr:    true,
			wantText:   "t.c:3:1: broken",
		},
		{
			name: "a warning ahead of an error",
			build: func() source.DiagnosticList {
				var l source.DiagnosticList
				l.Warnf(pos, "retired")
				l.Addf(pos, "broken")
				return l
			},
			wantErrors: 1,
			wantErr:    true,
			wantText:   "t.c:3:1: warning: retired\nt.c:3:1: broken",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := tt.build()
			if got := list.Errors(); got != tt.wantErrors {
				t.Errorf("Errors() = %d, want %d", got, tt.wantErrors)
			}
			if got := list.HasErrors(); got != tt.wantErr {
				t.Errorf("HasErrors() = %v, want %v", got, tt.wantErr)
			}
			if got := list.Err() != nil; got != tt.wantErr {
				t.Errorf("Err() non-nil = %v, want %v", got, tt.wantErr)
			}
			if got := list.String(); got != tt.wantText {
				t.Errorf("String() = %q, want %q", got, tt.wantText)
			}
		})
	}
}
