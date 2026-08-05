package isel

import (
	"fmt"
	"testing"
)

// TestStatementsBelowATerminatorDoNotRun holds a program carrying unreachable
// statements to what the reachable ones say. Each unreachable statement writes a
// value no reachable path produces, so a run that executed one is visible on the
// device rather than only in the instruction text.
func TestStatementsBelowATerminatorDoNotRun(t *testing.T) {
	cases := []struct {
		name string
		body string
		want float64
	}{
		{
			name: "a break leaves the rest of the body out",
			body: "for (long long i = 0; i < 7; i++) { total += 10; break; total += 1000; }",
			want: 10,
		},
		{
			name: "a continue leaves the rest of the body out",
			body: "for (long long i = 0; i < 3; i++) { total += 10; continue; total += 1000; }",
			want: 30,
		},
		{
			name: "a return leaves the rest of a callee out",
			body: "total += reached();",
			want: 7,
		},
		{
			name: "a switch arm stops at its break",
			body: "switch (total) { case 0: total += 5; break; total += 1000; default: total += 1000; }",
			want: 5,
		},
		{
			name: "a nested loop below a break is never built",
			body: "while (true) { total += 2; break; for (long long i = 0; i < 3; i++) { total += 1000; } }",
			want: 2,
		},
	}

	const shell = `const dev out = d1;

long long reached(void) {
    return 7;
    return 1000;
}

void main(void) {
    long long total = 0;
    %s
    __ic_store(out, Setting, (double)total);
    return;
    __ic_store(out, Setting, 1000.0);
}`

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, fmt.Sprintf(shell, tc.body))
			events := runWorld(t, assembly, func(*testing.T, *world) {}, 1)
			if len(events) != 1 {
				t.Fatalf("the program made %d writes, want the one the reachable statements ask for; it wrote %s\n%s",
					len(events), describeWrites(events), assembly)
			}
			assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
		})
	}
}
