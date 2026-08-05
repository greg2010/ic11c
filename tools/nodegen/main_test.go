package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCheckedInKindsAreRenderedFromTheCheckedInGrammar closes the second half of
// the codegen gate: internal/tsparse holds the checked-in grammar to the grammar
// module, and nothing else holds the Go tables to that grammar. Syncing the JSON
// without regenerating is otherwise silent, which is what a version bump leaves.
func TestCheckedInKindsAreRenderedFromTheCheckedInGrammar(t *testing.T) {
	// The default paths are rooted two levels above this package.
	const moduleRoot = "../.."
	nodeTypes := filepath.Join(moduleRoot, defaultNodeTypesPath)
	rules := filepath.Join(moduleRoot, defaultGrammarPath)
	kinds := filepath.Join(moduleRoot, defaultKindsPath)

	typesData, err := os.ReadFile(nodeTypes)
	if err != nil {
		t.Fatalf("reading %s: %v", nodeTypes, err)
	}
	types, err := decodeNodeTypes(typesData)
	if err != nil {
		t.Fatalf("decoding %s: %v", nodeTypes, err)
	}
	rulesData, err := os.ReadFile(rules)
	if err != nil {
		t.Fatalf("reading %s: %v", rules, err)
	}
	spellings, err := decodeSpellings(rulesData)
	if err != nil {
		t.Fatalf("decoding %s: %v", rules, err)
	}
	want, err := render(types, spellings)
	if err != nil {
		t.Fatalf("rendering %s: %v", nodeTypes, err)
	}
	got, err := os.ReadFile(kinds)
	if err != nil {
		t.Fatalf("reading %s: %v", kinds, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is not what %s and %s render to; run: go run ./tools/nodegen generate", kinds, nodeTypes, rules)
	}
}

func TestCamel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"one word", "declaration", "Declaration"},
		{"two words", "binary_expression", "BinaryExpression"},
		{"three words", "abstract_array_declarator", "AbstractArrayDeclarator"},
		{"hidden rule", "_declarator", "Declarator"},
		{"digit inside a word", "u8_prefix", "U8Prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := camel(strings.TrimPrefix(tt.in, "_"))
			if err != nil {
				t.Fatalf("camel(%q) failed: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("camel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCamelRefusesWhatItCannotName(t *testing.T) {
	for _, in := range []string{"", "_", "__", "+"} {
		t.Run(in, func(t *testing.T) {
			if got, err := camel(in); err == nil {
				t.Errorf("camel(%q) = %q, want an error", in, got)
			}
		})
	}
}

func TestSpellOut(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"keyword", "const", "Const"},
		{"operator", "+", "Plus"},
		{"compound operator", "<<=", "LtLtEq"},
		{"two brackets", "[[", "LbrackLbrack"},
		{"scope", "::", "ColonColon"},
		{"directive", "#include", "HashInclude"},
		{"reserved keyword", "_Atomic", "UnderAtomic"},
		{"decorated keyword", "__attribute__", "UnderUnderAttributeUnderUnder"},
		{"newline", "\n", "Newline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := spellOut(tt.in)
			if err != nil {
				t.Fatalf("spellOut(%q) failed: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("spellOut(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSpellOutRefusesAByteItCannotName is what makes the derivation total: an
// unaccounted byte stops the build rather than arriving as an unnameable constant.
func TestSpellOutRefusesAByteItCannotName(t *testing.T) {
	for _, in := range []string{"", "@", "a$b", "\t"} {
		t.Run(in, func(t *testing.T) {
			if got, err := spellOut(in); err == nil {
				t.Errorf("spellOut(%q) = %q, want an error", in, got)
			}
		})
	}
}

// TestKindIdentsRefuseACollision covers the other half: two kinds deriving one
// name render a constant block that does not compile, and the message says which.
func TestKindIdentsRefuseACollision(t *testing.T) {
	_, err := kindIdents([]nodeType{
		{Type: "widget", Named: true},
		{Type: "Widget", Named: true},
	})
	if err == nil {
		t.Fatal("two kinds deriving one name were accepted")
	}
	for _, want := range []string{"widget", "Widget", "KindWidget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

func TestDecodeNodeTypes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "a kind", in: `[{"type":"declaration","named":true}]`},
		{name: "empty", in: `[]`, wantErr: true},
		{name: "an unmodelled key", in: `[{"type":"declaration","named":true,"flavour":"new"}]`, wantErr: true},
		{name: "not a list", in: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeNodeTypes([]byte(tt.in))
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Errorf("decodeNodeTypes(%s) error = %v, want an error: %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// TestDecodeSpellings covers the reach the node types do not have. A grammar
// either writes a word as a token of its own or buries it in one whose node it
// names, and the second is where C keeps TRUE and size_t.
func TestDecodeSpellings(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{
			name: "a token of its own",
			in:   `{"rules":{"declaration":{"type":"STRING","value":"const"}}}`,
			want: []string{"const"},
		},
		{
			name: "a choice inside a named token",
			in:   `{"rules":{"true":{"type":"TOKEN","content":{"type":"CHOICE","members":[{"type":"STRING","value":"TRUE"},{"type":"STRING","value":"true"}]}}}}`,
			want: []string{"TRUE", "true"},
		},
		{
			name: "a word written in two rules",
			in:   `{"rules":{"a":{"type":"STRING","value":"if"},"b":{"type":"SEQ","members":[{"type":"STRING","value":"if"}]}}}`,
			want: []string{"if"},
		},
		{
			name:    "a pattern is no spelling",
			in:      `{"rules":{"identifier":{"type":"PATTERN","value":"[a-z]+"}}}`,
			wantErr: true,
		},
		{name: "not JSON", in: `{`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeSpellings([]byte(tt.in))
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("decodeSpellings(%s) error = %v, want an error: %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && !slices.Equal(got, tt.want) {
				t.Errorf("decodeSpellings(%s) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestGenerateWritesWhatItRendered covers the path the codegen gate drives: two
// grammar files in, a Go file out, under a directory that does not exist yet.
func TestGenerateWritesWhatItRendered(t *testing.T) {
	dir := t.TempDir()
	nodeTypes := writeJSON(t, dir, "node-types.json", `[{"type":"declaration","named":true},{"type":";"}]`)
	rules := writeJSON(t, dir, "grammar.json", `{"rules":{"true":{"type":"TOKEN","content":{"type":"STRING","value":"TRUE"}}}}`)
	out := filepath.Join(dir, "rendered", "nodekinds.gen.go")
	if err := generate(nodeTypes, rules, out); err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the output: %v", err)
	}
	for _, want := range []string{generatedHeader, "package tsnode", "KindDeclaration", "KindSemi", `"TRUE"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the output does not carry %q", want)
		}
	}
}

func TestGenerateReportsWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	nodeTypes := writeJSON(t, dir, "node-types.json", `[{"type":"declaration","named":true}]`)
	rules := writeJSON(t, dir, "grammar.json", `{"rules":{"if":{"type":"STRING","value":"if"}}}`)
	absent := filepath.Join(dir, "absent.json")
	out := filepath.Join(dir, "out.go")

	tests := []struct {
		name             string
		nodeTypes, rules string
	}{
		{name: "no node types", nodeTypes: absent, rules: rules},
		{name: "no rules", nodeTypes: nodeTypes, rules: absent},
		{
			name:      "a kind with no name",
			nodeTypes: writeJSON(t, dir, "unnameable.json", `[{"type":"@"}]`),
			rules:     rules,
		},
		{
			name:      "rules holding no literal",
			nodeTypes: nodeTypes,
			rules:     writeJSON(t, dir, "wordless.json", `{"rules":{"identifier":{"type":"PATTERN","value":"[a-z]+"}}}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := generate(tt.nodeTypes, tt.rules, out); err == nil {
				t.Error("generate accepted it")
			}
		})
	}
}

func writeJSON(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestRenderIsOrdered checks that rendering does not depend on the order the
// grammar listed its kinds in, so the codegen gate reports drift not iteration.
func TestRenderIsOrdered(t *testing.T) {
	types := []nodeType{
		{Type: "expression", Named: true, Subtypes: []typeRef{
			{Type: "identifier", Named: true},
			{Type: "binary_expression", Named: true},
		}},
		{Type: "binary_expression", Named: true, Fields: map[string]childSet{
			"operator": {Required: true, Types: []typeRef{{Type: "+"}, {Type: "-"}}},
		}, Children: &childSet{Multiple: true, Types: []typeRef{
			{Type: "identifier", Named: true},
			{Type: "expression", Named: true},
		}}},
		{Type: "identifier", Named: true},
		{Type: "+"},
		{Type: "-"},
	}
	first, err := render(types, []string{"+", "-"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	shuffled := []nodeType{types[3], types[2], types[1], types[4], types[0]}
	second, err := render(shuffled, []string{"+", "-"})
	if err != nil {
		t.Fatalf("render of the reordered grammar failed: %v", err)
	}
	if string(first) != string(second) {
		t.Error("reordering the grammar changed the rendering")
	}
	for _, want := range []string{"KindBinaryExpression", "KindPlus", "FieldOperator", "var Subtypes", "var ChildTypes", "var Spellings"} {
		if !strings.Contains(string(first), want) {
			t.Errorf("the rendering does not declare %s", want)
		}
	}
}

// TestRenderedChildTypesCarryTheGrammarsOwnList is what a walk over a node's
// children is held to. A named slot is checked against FieldTypes; everything
// else a node can hold is in this list and nowhere else.
func TestRenderedChildTypesCarryTheGrammarsOwnList(t *testing.T) {
	rendered, err := render([]nodeType{
		{Type: "type_descriptor", Named: true, Fields: map[string]childSet{
			"type": {Required: true, Types: []typeRef{{Type: "type_specifier", Named: true}}},
		}, Children: &childSet{Multiple: true, Types: []typeRef{{Type: "type_qualifier", Named: true}}}},
		{Type: "type_qualifier", Named: true},
		{Type: "type_specifier", Named: true},
		// A kind with no children of its own contributes no row: an empty one
		// would read as a walk having nothing to account for.
		{Type: ";"},
	}, []string{";"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	_, table, declared := strings.Cut(string(rendered), "var ChildTypes = map[Kind][]Kind{")
	if !declared {
		t.Fatalf("the rendering declares no ChildTypes:\n%s", rendered)
	}
	table, _, _ = strings.Cut(table, "\n}\n")
	want := "\n\tKindTypeDescriptor: {\n\t\tKindTypeQualifier,\n\t},"
	if table != want {
		t.Errorf("ChildTypes rendered\n\t%q\nwant\n\t%q", table, want)
	}
}

func TestSortedRefs(t *testing.T) {
	tests := []struct {
		name string
		refs []typeRef
		want []string
	}{
		{name: "no alternatives"},
		{
			name: "ordered by kind rather than by the grammar's listing",
			refs: []typeRef{{Type: "type_specifier"}, {Type: "identifier"}},
			want: []string{"identifier", "type_specifier"},
		},
		{
			// Without the dedupe a slot naming one kind twice renders two rows,
			// which every walk reading the table would account for twice.
			name: "one kind named twice",
			refs: []typeRef{{Type: "identifier"}, {Type: "type_specifier"}, {Type: "identifier"}},
			want: []string{"identifier", "type_specifier"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortedRefs(tt.refs); !slices.Equal(got, tt.want) {
				t.Errorf("sortedRefs(%v) = %v, want %v", tt.refs, got, tt.want)
			}
		})
	}
}
