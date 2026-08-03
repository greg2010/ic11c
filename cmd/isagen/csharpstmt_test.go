package main

import (
	"slices"
	"strings"
	"testing"
)

func TestIsKeywordStatement(t *testing.T) {
	tests := []struct {
		text    string
		keyword string
		want    bool
	}{
		{text: "if (a)", keyword: "if", want: true},
		{text: "if(a)", keyword: "if", want: true},
		{text: "ifx = 1", keyword: "if", want: false},
		{text: "return false;", keyword: "return", want: true},
		{text: "returnValue = 1;", keyword: "return", want: false},
		{text: "switch", keyword: "switch", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			if got := isKeywordStatement(tt.text, tt.keyword); got != tt.want {
				t.Errorf("isKeywordStatement(%q, %q) = %v, want %v", tt.text, tt.keyword, got, tt.want)
			}
		})
	}
}

func TestSplitIf(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantCond string
		wantBody string
		wantErr  string
	}{
		{
			name:     "braced body",
			text:     "if (a == LogicType.Power)\n{\n\treturn true;\n}",
			wantCond: "a == LogicType.Power",
			wantBody: "\n\treturn true;\n",
		},
		{
			name:     "nested parentheses in the condition",
			text:     "if (!(a && b))\n{\n\treturn false;\n}",
			wantCond: "!(a && b)",
			wantBody: "\n\treturn false;\n",
		},
		{name: "no condition", text: "if\n{\n}", wantErr: `opening "("`},
		{name: "no body", text: "if (a) return true;", wantErr: `opening "{"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, body, err := splitIf(tt.text)
			if tt.wantErr != "" {
				checkErr(t, "splitIf", err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("splitIf: %v", err)
			}
			if cond != tt.wantCond || body != tt.wantBody {
				t.Errorf("splitIf = (%q, %q), want (%q, %q)", cond, body, tt.wantCond, tt.wantBody)
			}
		})
	}
}

// TestSplitSwitchBody covers the grouping the game depends on most: a run of
// labels with nothing between them reaches one arm, and a wide property group
// is written exactly that way.
func TestSplitSwitchBody(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantLabels [][]string
		wantErr    string
	}{
		{
			name: "labels sharing an arm",
			body: "\ncase LogicType.Power:\ncase LogicType.Open:\n\treturn true;\ndefault:\n\treturn false;\n",
			wantLabels: [][]string{
				{"LogicType.Power", "LogicType.Open"},
				{"default"},
			},
		},
		{
			name: "arm declaring a local in a block",
			body: "\ncase LogicSlotType.Quantity:\n{\n\tSlot.Class type = slot.Type;\n\treturn type == Slot.Class.Helmet;\n}\ndefault:\n\treturn false;\n",
			wantLabels: [][]string{
				{"LogicSlotType.Quantity"},
				{"default"},
			},
		},
		{
			name: "default alone",
			body: "\ndefault:\n\treturn false;\n",
			wantLabels: [][]string{
				{"default"},
			},
		},
		{name: "no arms", body: "\n", wantErr: "no arms"},
		{name: "label with no colon", body: "\ncase LogicType.Power\n\treturn true;\n", wantErr: "no colon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups, err := splitSwitchBody(tt.body)
			if tt.wantErr != "" {
				checkErr(t, "splitSwitchBody", err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("splitSwitchBody: %v", err)
			}
			var labels [][]string
			for _, group := range groups {
				labels = append(labels, group.labels)
				if strings.TrimSpace(group.body) == "" {
					t.Errorf("arm %v has an empty body", group.labels)
				}
			}
			if !slices.EqualFunc(labels, tt.wantLabels, slices.Equal) {
				t.Errorf("labels = %v, want %v", labels, tt.wantLabels)
			}
		})
	}
}

func TestSplitSwitchExpr(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantOK  bool
		want    []switchArm
		wantErr string
	}{
		{
			name:   "arms and a discard",
			expr:   "logicType switch\n{\n\tLogicType.Open => HasOpenState, \n\t_ => false, \n}",
			wantOK: true,
			want: []switchArm{
				{label: "LogicType.Open", result: "HasOpenState"},
				{label: "_", result: "false"},
			},
		},
		{name: "not a switch expression", expr: "HasOpenState", wantOK: false},
		{
			name:    "arm with no result",
			expr:    "logicType switch\n{\n\tLogicType.Open, \n}",
			wantErr: "has no result",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, arms, ok, err := splitSwitchExpr(tt.expr)
			if tt.wantErr != "" {
				checkErr(t, "splitSwitchExpr", err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("splitSwitchExpr: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("splitSwitchExpr recognized = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if subject != "logicType" {
				t.Errorf("subject = %q, want %q", subject, "logicType")
			}
			if !slices.Equal(arms, tt.want) {
				t.Errorf("arms = %+v, want %+v", arms, tt.want)
			}
		})
	}
}

// TestCutTopOperator covers the one thing operator splitting has to get right:
// an operator that is a prefix of a longer one must not be split on, or the
// arrow of a switch arm reads as an equality and a range test as a relation.
func TestCutTopOperator(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		op       string
		wantOK   bool
		wantLHS  string
		wantRHS  string
		wantSkip bool
	}{
		{name: "equality", expr: "a == b", op: "==", wantOK: true, wantLHS: "a", wantRHS: "b"},
		{name: "inequality is not equality", expr: "a != b", op: "==", wantOK: false},
		{name: "at least is not greater", expr: "a >= b", op: ">", wantOK: false},
		{name: "at least", expr: "a >= b", op: ">=", wantOK: true, wantLHS: "a", wantRHS: "b"},
		{name: "arrow is not equality", expr: "a => b", op: "==", wantOK: false},
		{name: "inside parentheses", expr: "f(a == b)", op: "==", wantOK: false},
		{name: "subtraction", expr: "logicType - 20", op: "-", wantOK: true, wantLHS: "logicType", wantRHS: "20"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lhs, rhs, ok := cutTopOperator(tt.expr, tt.op)
			if ok != tt.wantOK {
				t.Fatalf("cutTopOperator(%q, %q) found = %v, want %v", tt.expr, tt.op, ok, tt.wantOK)
			}
			if ok && (lhs != tt.wantLHS || rhs != tt.wantRHS) {
				t.Errorf("cutTopOperator(%q, %q) = (%q, %q), want (%q, %q)", tt.expr, tt.op, lhs, rhs, tt.wantLHS, tt.wantRHS)
			}
		})
	}
}

// TestSplitExprList covers what separates it from splitTop: the arrow of a
// switch arm must not unbalance the list.
func TestSplitExprList(t *testing.T) {
	tests := []struct {
		name string
		list string
		want []string
	}{
		{name: "switch arms", list: "A => x, B => y, ", want: []string{"A => x", " B => y"}},
		{name: "nested call", list: "f(a, b), c", want: []string{"f(a, b)", " c"}},
		{name: "string holding a comma", list: `"a, b", c`, want: []string{`"a, b"`, " c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitExprList(tt.list); !slices.Equal(got, tt.want) {
				t.Errorf("splitExprList(%q) = %q, want %q", tt.list, got, tt.want)
			}
		})
	}
}

func TestStripOuterParens(t *testing.T) {
	tests := []struct{ expr, want string }{
		{expr: "(a)", want: "a"},
		{expr: "((a))", want: "a"},
		{expr: "(a) && (b)", want: "(a) && (b)"},
		{expr: "f(a)", want: "f(a)"},
		{expr: "a", want: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			if got := stripOuterParens(tt.expr); got != tt.want {
				t.Errorf("stripOuterParens(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestUnwrapBlock(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{name: "one block", body: "\n{\n\treturn true;\n}\n", want: "\n\treturn true;\n"},
		{name: "statements", body: "\nreturn true;\n", want: "\nreturn true;\n"},
		{name: "block followed by more", body: "\n{\n\tint a;\n}\nreturn true;\n", want: "\n{\n\tint a;\n}\nreturn true;\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unwrapBlock(tt.body); got != tt.want {
				t.Errorf("unwrapBlock(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}
