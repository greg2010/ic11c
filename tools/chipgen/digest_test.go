package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclDigest(t *testing.T) {
	const body = "public int A()\n{\n\treturn 1;\n}"
	tests := []struct {
		name string
		a, b string
		same bool
	}{
		{name: "the same text", a: body, b: body, same: true},
		{name: "re-indented", a: body, b: "\tpublic int A()\n\t{\n\t\treturn 1;\n\t}", same: true},
		{name: "blank lines", a: body, b: "public int A()\n{\n\n\treturn 1;\n\n}", same: true},
		{
			name: "a comment above it",
			a:    body,
			b:    "// verbatim: Assets/Scripts/Objects/Thing.cs:120-140\n" + body,
			same: true,
		},
		{
			name: "the line range in that comment",
			a:    "// verbatim: Thing.cs:120-140\n" + body,
			b:    "// verbatim: Thing.cs:900-920\n" + body,
			same: true,
		},
		{name: "a changed value", a: body, b: "public int A()\n{\n\treturn 2;\n}"},
		{name: "a changed signature", a: body, b: "public int B()\n{\n\treturn 1;\n}"},
		{
			name: "a comment marker inside a string literal",
			a:    `public string A = "//a";`,
			b:    `public string A = "//b";`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := declDigest(test.a) == declDigest(test.b); got != test.same {
				t.Errorf("declDigest(a) == declDigest(b) = %v, want %v", got, test.same)
			}
		})
	}
}

func TestParseDigest(t *testing.T) {
	const good = "# a comment\nattributes One Two\nrecords 2\n\n" +
		"cut aaaaaaaaaaaaaaaa A.cs/public int B\nshape bbbbbbbbbbbbbbbb A.cs\n"
	tests := []struct {
		name    string
		text    string
		wantErr string
	}{
		{name: "a whole file", text: good},
		{name: "truncated", text: strings.TrimSuffix(good, "shape bbbbbbbbbbbbbbbb A.cs\n"), wantErr: "holds 1"},
		{name: "no count at all", text: strings.Replace(good, "records 2\n", "", 1), wantErr: "no record count"},
		{name: "empty", text: "records 0\n", wantErr: "holds no records"},
		{name: "an unreadable count", text: strings.Replace(good, "records 2", "records many", 1), wantErr: "not a number"},
		{name: "a kind nothing writes", text: strings.Replace(good, "cut ", "trace ", 1), wantErr: "unknown record kind"},
		{name: "a record naming nothing", text: strings.Replace(good, " A.cs/public int B", "", 1), wantErr: "names no path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseDigest(test.text)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseDigest error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDigest: %v", err)
			}
			if len(got.records) != 2 || got.attributes != "One Two" {
				t.Errorf("parseDigest = %d records, attributes %q; want 2 and %q", len(got.records), got.attributes, "One Two")
			}
		})
	}
}

func TestCompareDigest(t *testing.T) {
	base := func(t *testing.T) *ledger {
		t.Helper()
		l := newLedger()
		for _, r := range []record{
			{kind: cutKind, path: "A.cs/public int B", digest: "1111111111111111"},
			{kind: shapeKind, path: "A.cs", digest: "2222222222222222"},
		} {
			if err := l.add(r.kind, r.path, r.digest); err != nil {
				t.Fatalf("add: %v", err)
			}
		}
		return l
	}
	want, err := parseDigest("attributes One\nrecords 2\n" +
		"cut 1111111111111111 A.cs/public int B\nshape 2222222222222222 A.cs\n")
	if err != nil {
		t.Fatalf("parseDigest: %v", err)
	}

	tests := []struct {
		name    string
		change  func(*testing.T, *ledger)
		dropped []string
		wantErr string
	}{
		{name: "the same decompile", dropped: []string{"One"}},
		{
			name: "a body that changed under its own signature",
			change: func(_ *testing.T, l *ledger) {
				l.records[cutKind+" A.cs/public int B"] = record{kind: cutKind, path: "A.cs/public int B", digest: "9999999999999999"}
			},
			dropped: []string{"One"},
			wantErr: "1 changed since the digest was taken:\n\tcut A.cs/public int B",
		},
		{
			name: "a construct the slice did not cut before",
			change: func(t *testing.T, l *ledger) {
				if err := l.add(cutKind, "A.cs/public int C", "3333333333333333"); err != nil {
					t.Fatalf("add: %v", err)
				}
			},
			dropped: []string{"One"},
			wantErr: "1 cut now and not named by the digest:\n\tcut A.cs/public int C",
		},
		{
			name: "a construct the slice stopped cutting",
			change: func(_ *testing.T, l *ledger) {
				delete(l.records, cutKind+" A.cs/public int B")
			},
			dropped: []string{"One"},
			wantErr: "1 named by the digest and not cut now:\n\tcut A.cs/public int B",
		},
		{
			name:    "an attribute nothing dropped before",
			dropped: []string{"One", "Two"},
			wantErr: `attributes dropped from the lifted declarations are now "One Two"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			l := base(t)
			if test.change != nil {
				test.change(t, l)
			}
			dropped := make(map[string]bool, len(test.dropped))
			for _, name := range test.dropped {
				dropped[name] = true
			}
			err := compareDigest(l, dropped, want)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("compareDigest: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("compareDigest error = %v, want one containing %q", err, test.wantErr)
			}
		})
	}
}

func TestTheCheckedInDigestCarriesTheGeneratedHeader(t *testing.T) {
	if !strings.HasPrefix(checkedDigest, digestHeader) {
		t.Errorf("%s does not open with digestHeader, which parseDigest drops with every other "+
			"# line and holds to nothing; rewrite it with --update-digest", digestFile)
	}
}

// The digest has to cover the constructs the oracle's answers turn on. They are
// named rather than counted: a count is met by any thousand records.
func TestTheDigestCoversWhatTheChipAnswersWith(t *testing.T) {
	want, err := parseDigest(checkedDigest)
	if err != nil {
		t.Fatalf("parseDigest: %v", err)
	}
	for _, path := range []string{
		cutKind + " " + chipPath + "/public void Execute(int runCount)",
		cutKind + " " + chipPath + "/public void SetSourceCode(string sourceCode)",
		cutKind + " " + chipPath + "/public static long DoubleToLong(double d, bool signed)",
		cutKind + " " + chipPath + "/_ADD_Operation/public override int Execute(int index)",
		cutKind + " " + chipPath + "/_LineOfCode",
		cutKind + " " + chipPath + "/" + hcfOperation,
		cutKind + " " + devicePath + "/public virtual void SetLogicValue(LogicType logicType, double value)",
		cutKind + " " + devicePath + "/public virtual bool CanLogicRead(LogicType logicType)",
		cutKind + " " + housingPath + "/public ILogicable GetLogicableFromIndex(int deviceIndex, int networkIndex = int.MinValue)",
		cutKind + " " + thingPath + "/public virtual int ColorState",
		cutKind + " " + reagentPath + "/public static Reagent Find(int hash)",
		shapeKind + " " + chipPath,
		shapeKind + " " + smallGridPath,
		shapeKind + " " + structurePath,
	} {
		if _, ok := want.records[path]; !ok {
			t.Errorf("%s names no record for %q", digestFile, path)
		}
	}
}

func TestStaleUnits(t *testing.T) {
	written := map[string]bool{chipFile: true, sourcesFile: true}
	tests := []struct {
		name    string
		present []string
		want    []string
		wantErr string
	}{
		{name: "a fresh directory"},
		{name: "only what this run writes", present: []string{chipFile, sourcesFile}},
		{name: "an earlier run's leftover", present: []string{chipFile, sourcesFile, "Old.cs"}, want: []string{"Old.cs"}},
		{name: "something that is not C#", present: []string{chipFile, sourcesFile, "notes.txt"}},
		{name: "C# beside no record of an earlier run", present: []string{"Mine.cs"}, wantErr: "did not write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := t.TempDir()
			for _, name := range test.present {
				if err := os.WriteFile(filepath.Join(out, name), nil, 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			got, err := staleUnits(out, written)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("staleUnits error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("staleUnits: %v", err)
			}
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Errorf("staleUnits = %q, want %q", got, test.want)
			}
		})
	}
}

// The slice writes exactly the units its response file names, and removes what an
// earlier one left behind. Two such lists held to nothing drift apart.
func TestSliceLeavesOnlyWhatItWrote(t *testing.T) {
	requireDecompile(t, chipPath)
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, sourcesFile), []byte("Gone.cs\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", sourcesFile, err)
	}
	if err := os.WriteFile(filepath.Join(out, "Gone.cs"), []byte("// left by an earlier slice\n"), 0o644); err != nil {
		t.Fatalf("write Gone.cs: %v", err)
	}
	if _, err := slice(gameSource, out, ""); err != nil {
		t.Fatalf("slice: %v", err)
	}

	sources, err := os.ReadFile(filepath.Join(out, sourcesFile))
	if err != nil {
		t.Fatalf("read %s: %v", sourcesFile, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	var onDisk []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".cs") {
			onDisk = append(onDisk, entry.Name())
		}
	}
	for _, name := range onDisk {
		if !strings.Contains(string(sources), name+"\n") {
			t.Errorf("%s is on disk and %s does not name it", name, sourcesFile)
		}
	}
	for _, name := range []string{chipFile, supportFile, deviceFile, reagentFile, harnessFile} {
		if !strings.Contains(string(sources), name+"\n") {
			t.Errorf("%s does not name %s", sourcesFile, name)
		}
	}
	if len(onDisk) != len([]string{chipFile, supportFile, deviceFile, reagentFile, harnessFile}) {
		t.Errorf("%s holds %q, want only the units of this run", out, onDisk)
	}
}

// The whole point of the digest, run against the decompile itself. Each case
// changes one construct and leaves it findable under its own signature, which is
// the shape of game update nothing else notices: the last two change nothing in
// the emitted unit and are still a chip that answers differently.
func TestTheDigestNoticesAChangedBody(t *testing.T) {
	requireDecompile(t, chipPath)
	tests := []struct {
		name string
		file string
		old  string
		new  string
		want string
	}{
		{
			name: "an operation body",
			file: chipPath,
			old:  "_Chip._Registers[variableIndex] = variableValue + variableValue2;",
			new:  "_Chip._Registers[variableIndex] = variableValue - variableValue2;",
			want: cutKind + " " + chipPath + "/_ADD_Operation/public override int Execute(int index)",
		},
		{
			name: "a device permission arm",
			file: devicePath,
			old:  "LogicType.Color => HasColorState,",
			new:  "LogicType.Color => false,",
			want: cutKind + " " + devicePath + "/public virtual bool CanLogicWrite(LogicType logicType)",
		},
		{
			name: "an enum ordinal",
			file: "Assets/Scripts/Objects/Motherboards/LogicType.cs",
			old:  "\tOpen = 2,",
			new:  "\tOpen = 3,",
			want: cutKind + " Assets/Scripts/Objects/Motherboards/LogicType.cs/LogicType",
		},
		{
			name: "a member no keep list names",
			file: chipPath,
			old:  "\tpublic const char HEX_CHAR",
			new:  "\tpublic int SomethingNew;\n\n\tpublic const char HEX_CHAR",
			want: shapeKind + " " + chipPath,
		},
		{
			name: "the body of a construct the slice drops",
			file: chipPath,
			old:  "\t\t\tdevice.SetLogicValue(logicType, value);",
			new:  "\t\t\tdevice.SetLogicValue(logicType, value + 1.0);",
			want: cutKind + " " + chipPath + "/_Operation/" + setDeviceValueSignature,
		},
		{
			name: "the branch a lifted accessor stands a constant in for",
			file: thingPath,
			old:  "&& HasColorState) ? BaseAnimator.GetInteger(Interactable.ColorState) : 0);",
			new:  "&& HasColorState) ? BaseAnimator.GetInteger(Interactable.ColorState) : 1);",
			want: cutKind + " " + thingPath + "/public virtual int ColorState",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := copyReadSet(t)
			if _, err := slice(root, filepath.Join(t.TempDir(), "out"), ""); err != nil {
				t.Fatalf("slice over an unchanged copy of the read set: %v", err)
			}
			mutate(t, filepath.Join(root, filepath.FromSlash(test.file)), test.old, test.new)

			_, err := slice(root, filepath.Join(t.TempDir(), "out"), "")
			if err == nil {
				t.Fatalf("slice over a changed %s succeeded", test.file)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("slice error does not name %q:\n%v", test.want, err)
			}
		})
	}
}

// copyReadSet copies every file the slice reads into a tree of its own. The list
// comes out of the checked-in digest rather than from one written here, so a file
// the slicer starts reading is copied without anything being told about it.
func copyReadSet(t *testing.T) string {
	t.Helper()
	want, err := parseDigest(checkedDigest)
	if err != nil {
		t.Fatalf("parseDigest: %v", err)
	}
	root := t.TempDir()
	copied := 0
	for _, r := range want.records {
		if r.kind != shapeKind {
			continue
		}
		data, err := os.ReadFile(filepath.Join(gameSource, filepath.FromSlash(r.path)))
		if err != nil {
			t.Fatalf("read %s: %v", r.path, err)
		}
		path := filepath.Join(root, filepath.FromSlash(r.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		copied++
	}
	if copied == 0 {
		t.Fatalf("%s names no file to copy", digestFile)
	}
	return root
}

// mutate rewrites the single occurrence of old in the file at path.
func mutate(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got := strings.Count(text, old); got != 1 {
		t.Fatalf("%s holds %d occurrences of %q, want 1", path, got, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(text, old, replacement, 1)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
