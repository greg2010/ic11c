package main

import (
	"strings"
	"testing"
)

// The eight state accessors are the one place the slice takes a branch out of a
// method rather than the whole method, so a getter rewritten around a different
// flag must stop the slice rather than specialize something nobody read.
func TestBlockAfter(t *testing.T) {
	const getter = `public virtual bool OnOff
{
	get
	{
		if (HasOnOffState && !HasBaseAnimator)
		{
			return InteractOnOff.State == 1;
		}
		if (ThreadedManager.IsThread)
		{
			return _onOff;
		}
		_onOff = false;
		return _onOff;
	}
}`

	tests := []struct {
		name    string
		text    string
		head    string
		want    string
		wantErr string
	}{
		{
			name: "the branch as it stands",
			text: getter,
			head: "if (HasOnOffState && !HasBaseAnimator)",
			want: "\t\t\treturn InteractOnOff.State == 1;",
		},
		{
			name: "a nested block keeps its own shape",
			text: "if (a)\n{\n\tif (b)\n\t{\n\t\treturn 1;\n\t}\n\treturn 2;\n}",
			head: "if (a)",
			want: "\t\t\tif (b)\n\t\t\t{\n\t\t\t\treturn 1;\n\t\t\t}\n\t\t\treturn 2;",
		},
		{
			name:    "a condition that no longer appears",
			text:    getter,
			head:    "if (HasOnOffState && !HasAnimator)",
			wantErr: "not found",
		},
		{
			name:    "a condition that appears twice",
			text:    getter + getter,
			head:    "if (HasOnOffState && !HasBaseAnimator)",
			wantErr: "more than once",
		},
		{
			name:    "a branch that lost its block",
			text:    "if (a) return 1;",
			head:    "if (a)",
			wantErr: "not found",
		},
		{
			name:    "an empty branch",
			text:    "if (a)\n{\n}\n",
			head:    "if (a)",
			wantErr: "empty block",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := blockAfter(test.text, test.head)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("blockAfter error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("blockAfter: %v", err)
			}
			if got != test.want {
				t.Fatalf("blockAfter = %q, want %q", got, test.want)
			}
		})
	}
}
