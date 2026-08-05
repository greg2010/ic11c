package main

import (
	"errors"
	"strings"
	"testing"
)

func TestReplaceExactly(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		old, repl   string
		want        int
		wantText    string
		wantErrText string
	}{
		{name: "one occurrence", src: "a b a", old: "b", repl: "c", want: 1, wantText: "a c a"},
		{name: "every occurrence", src: "a b a", old: "a", repl: "c", want: 2, wantText: "c b c"},
		{name: "removal", src: "a b", old: " b", want: 1, wantText: "a"},
		{name: "no occurrence refuses", src: "a", old: "b", want: 1, wantErrText: "found 0"},
		{name: "too many refuses", src: "a a", old: "a", want: 1, wantErrText: "found 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := replaceExactly(test.src, test.old, test.repl, test.want, "test")
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("replaceExactly error = %v, want one containing %q", err, test.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("replaceExactly: %v", err)
			}
			if got != test.wantText {
				t.Errorf("replaceExactly = %q, want %q", got, test.wantText)
			}
		})
	}
}

func TestCutStatement(t *testing.T) {
	const src = "{\n\tA = new List<int> { 1, 2 };\n\tB = 3;\n}"
	tests := []struct {
		name     string
		prefix   string
		wantText string
		wantErr  string
	}{
		{name: "statement with a nested initializer", prefix: "A = ", wantText: "{\n\tB = 3;\n}"},
		{name: "plain statement", prefix: "B = ", wantText: "{\n\tA = new List<int> { 1, 2 };\n}"},
		{name: "absent statement refuses", prefix: "C = ", wantErr: "not found"},
		{name: "repeated statement refuses", prefix: " = ", wantErr: "more than once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := cutStatement(src, test.prefix)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("cutStatement error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("cutStatement: %v", err)
			}
			if got != test.wantText {
				t.Errorf("cutStatement = %q, want %q", got, test.wantText)
			}
		})
	}
}

func TestCutStatementRefusesUnterminated(t *testing.T) {
	if _, err := cutStatement("{\n\tA = 1\n", "A = "); err == nil {
		t.Fatal("cutStatement over an unterminated statement returned no error")
	}
}

func TestCutBlock(t *testing.T) {
	const src = "{\n\tif (x)\n\t{\n\t\tA();\n\t}\n\tB();\n}"
	got, err := cutBlock(src, "if (x)")
	if err != nil {
		t.Fatalf("cutBlock: %v", err)
	}
	if want := "{\n\tB();\n}"; got != want {
		t.Errorf("cutBlock = %q, want %q", got, want)
	}
	if _, err := cutBlock(src, "if (y)"); !errors.Is(err, errNotFound) {
		t.Errorf("cutBlock over an absent block error = %v, want errNotFound", err)
	}
}

func TestDropParseArm(t *testing.T) {
	const body = `private class _LineOfCode
{
	public void Parse()
	{
		switch (command)
		{
			case ScriptCommand.hcf:
				Operation = new _HCF_Operation(chip, lineNumber);
				break;
			case ScriptCommand.yield:
				Operation = new _YIELD_Operation(chip, lineNumber);
				break;
			default:
				break;
		}
	}
}`
	decls, err := splitDecls(body)
	if err != nil {
		t.Fatalf("splitDecls: %v", err)
	}
	got, err := dropParseArm(decls[0], "hcf")
	if err != nil {
		t.Fatalf("dropParseArm: %v", err)
	}
	if strings.Contains(got, "_HCF_Operation") {
		t.Errorf("dropped arm still constructs the operation:\n%s", got)
	}
	if !strings.Contains(got, "case ScriptCommand.yield:") {
		t.Errorf("dropped arm took the next one with it:\n%s", got)
	}
	if _, err := dropParseArm(decls[0], "sleep"); !errors.Is(err, errNotFound) {
		t.Errorf("dropParseArm over an absent arm error = %v, want errNotFound", err)
	}
}

func TestDropParseArmRefusesWithoutAFollowingArm(t *testing.T) {
	const body = `private class _LineOfCode
{
	public void Parse()
	{
		switch (command)
		{
			case ScriptCommand.hcf:
				break;
		}
	}
}`
	decls, err := splitDecls(body)
	if err != nil {
		t.Fatalf("splitDecls: %v", err)
	}
	if _, err := dropParseArm(decls[0], "hcf"); !errors.Is(err, errNotFound) {
		t.Errorf("dropParseArm error = %v, want errNotFound", err)
	}
}

func TestDropMembers(t *testing.T) {
	const body = `private class Outer
{
	public int A;

	protected void B(int x)
	{
		C();
	}

	public int D;
}`
	decls, err := splitDecls(body)
	if err != nil {
		t.Fatalf("splitDecls: %v", err)
	}
	tests := []struct {
		name       string
		signatures []string
		absent     []string
		present    []string
	}{
		{
			name:       "one member",
			signatures: []string{"protected void B(int x)"},
			absent:     []string{"void B(", "C();"},
			present:    []string{"public int A;", "public int D;"},
		},
		{
			// The removals overlap in offset: taking A out first would move B,
			// so a caller naming them in source order must still get both.
			name:       "an earlier member and a later one",
			signatures: []string{"public int A", "protected void B(int x)"},
			absent:     []string{"public int A;", "void B(", "C();"},
			present:    []string{"public int D;"},
		},
		{
			name:       "named in the other order",
			signatures: []string{"protected void B(int x)", "public int A"},
			absent:     []string{"public int A;", "void B(", "C();"},
			present:    []string{"public int D;"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := dropMembers(decls[0], test.signatures...)
			if err != nil {
				t.Fatalf("dropMembers: %v", err)
			}
			for _, absent := range test.absent {
				if strings.Contains(got, absent) {
					t.Errorf("dropMembers left %q behind:\n%s", absent, got)
				}
			}
			for _, present := range test.present {
				if !strings.Contains(got, present) {
					t.Errorf("dropMembers removed %q:\n%s", present, got)
				}
			}
		})
	}

	if _, err := dropMembers(decls[0], "protected void E()"); !errors.Is(err, errNotFound) {
		t.Errorf("dropMembers over an absent member error = %v, want errNotFound", err)
	}
	_, err = dropMembers(decls[0], "public int A", "public int A")
	if err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Errorf("dropMembers over one member named twice error = %v, want one containing %q", err, "overlapping")
	}
}

// executeShape is Execute reduced to the four landmarks the edit reads: the guard
// it clears the flag ahead of, the tick loop, and the three endings that leave
// the loop early.
const executeShape = `public void Execute(int runCount)
{
	` + executeGuard + `
	{
		return;
	}
	` + executeLoop + `
	{
		try
		{
			_NextAddr = _LinesOfCode[_NextAddr].Operation.Execute(_NextAddr);
		}
		catch (ProgrammableChipException ex)
		{
			break;
		}
		catch (Exception)
		{
			break;
		}
		if (_NextAddr < 0)
		{
			_NextAddr = -_NextAddr;
			break;
		}
	}
}`

func TestEditExecute(t *testing.T) {
	decls, err := splitDecls(executeShape)
	if err != nil {
		t.Fatalf("splitDecls: %v", err)
	}
	got, err := editExecute(decls[0])
	if err != nil {
		t.Fatalf("editExecute: %v", err)
	}
	if strings.Contains(got, executeExit) {
		t.Errorf("an ending still leaves the loop with %q rather than the method:\n%s", executeExit, got)
	}
	cleared := strings.Index(got, "HarnessBudgetExhausted = false;")
	guard := strings.Index(got, executeGuard)
	record := strings.Index(got, executeRecord)
	loopEnd := strings.LastIndex(got, "\t}")
	switch {
	case cleared < 0 || guard < 0 || record < 0:
		t.Fatalf("editExecute did not write both halves of the record:\n%s", got)
	case cleared > guard:
		t.Errorf("the flag is cleared after the guard, so a refused run keeps the last one's answer:\n%s", got)
	case record < loopEnd:
		t.Errorf("the record is inside the tick loop, so it runs on every ending:\n%s", got)
	}
}

func TestEditExecuteRefusesAChangedLoop(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr string
	}{
		{
			name:    "a statement after the loop",
			text:    strings.Replace(executeShape, "\t}\n}", "\t}\n\t_NextAddr = 0;\n}", 1),
			wantErr: "followed by",
		},
		{
			name:    "an ending that no longer breaks",
			text:    strings.Replace(executeShape, "\t\t\tbreak;\n\t\t}\n\t\tcatch (Exception)", "\t\t\treturn;\n\t\t}\n\t\tcatch (Exception)", 1),
			wantErr: "found 2",
		},
		{
			name:    "a guard that moved",
			text:    strings.Replace(executeShape, executeGuard, "if (_LinesOfCode.Count == 0)", 1),
			wantErr: "found 0",
		},
		{
			name:    "a loop condition that moved",
			text:    strings.Replace(executeShape, executeLoop, "while (num-- > 0)", 1),
			wantErr: "not found",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decls, err := splitDecls(test.text)
			if err != nil {
				t.Fatalf("splitDecls: %v", err)
			}
			_, err = editExecute(decls[0])
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("editExecute error = %v, want one containing %q", err, test.wantErr)
			}
		})
	}
}

func TestEditRandOperation(t *testing.T) {
	const class = `private class _RAND_Operation : _Operation_1_0
{
	` + randSource + `;

	` + randSourceCtor + `
	{
		_RandomNumberGenerator = new Random();
	}

	public override int Execute(int index)
	{
		_Chip._Registers[0] = ` + randDraw + `;
		return index + 1;
	}
}`
	decls, err := splitDecls(class)
	if err != nil {
		t.Fatalf("splitDecls: %v", err)
	}
	got, err := editRandOperation(decls[0])
	if err != nil {
		t.Fatalf("editRandOperation: %v", err)
	}
	if strings.Contains(got, "_RandomNumberGenerator") {
		t.Errorf("the unseeded generator survives:\n%s", got)
	}
	if !strings.Contains(got, randRedirect) {
		t.Errorf("the draw was not redirected onto the seeded source:\n%s", got)
	}
	if !strings.Contains(got, "return index + 1;") {
		t.Errorf("editRandOperation removed more than the generator:\n%s", got)
	}
}

func TestTrimBlankRun(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "one blank line is kept", text: "a\n\nb", want: "a\n\nb"},
		{name: "two are collapsed to one", text: "a\n\n\nb", want: "a\n\nb"},
		{name: "many are collapsed to one", text: "a\n\n\n\n\nb", want: "a\n\nb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trimBlankRun(test.text); got != test.want {
				t.Errorf("trimBlankRun(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestKeptCommands(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{
			name: "every arm is listed once",
			body: "case ScriptCommand.l:\ncase ScriptCommand.s:\ndefault:",
			want: []string{"l", "s"},
		},
		{name: "a switch with no arms refuses", body: "return 0;", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := keptCommands(test.body)
			if test.wantErr {
				if err == nil {
					t.Fatalf("keptCommands(%q) = %q, want an error", test.body, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("keptCommands: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("keptCommands = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIsOperationClass(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "per-opcode class", in: "_MOVE_Operation", want: true},
		{name: "abstract root is not one", in: "_Operation", want: false},
		{name: "arity base is not one", in: "_Operation_1_0", want: false},
		{name: "unrelated nested type", in: "_LineOfCode", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isOperationClass(test.in); got != test.want {
				t.Errorf("isOperationClass(%q) = %v, want %v", test.in, got, test.want)
			}
		})
	}
}
