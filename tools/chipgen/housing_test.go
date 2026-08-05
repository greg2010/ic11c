package main

import (
	"maps"
	"strings"
	"testing"
)

// The chain check is what makes an emitted base call mean the body the game's
// would. Nothing else notices a game update moving a member onto a class the
// slice collapses away: the unit still compiles and answers through an unread body.
func TestCheckChain(t *testing.T) {
	const (
		housing = `public class CircuitHousing : LogicUnitBase, ICircuitHolder
{
	public bool IsValidIndex(int index)
	{
		return true;
	}
}
`
		logicUnitBase = `public class LogicUnitBase : SmallDevice, ILogicable
{
	public virtual double Setting
	{
		get
		{
			return 0.0;
		}
	}
}
`
		smallDevice = `public abstract class SmallDevice : Device
{
	public override int WreckageQuantity => 0;
}
`
	)
	tree := func(edits map[string]string) map[string]string {
		files := map[string]string{
			housingPath:       housing,
			logicUnitBasePath: logicUnitBase,
			smallDevicePath:   smallDevice,
		}
		maps.Copy(files, edits)
		return files
	}

	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{name: "the chain as it stands", files: tree(nil)},
		{
			name: "a housing that no longer derives from LogicUnitBase",
			files: tree(map[string]string{
				housingPath: strings.Replace(housing, ": LogicUnitBase,", ": Device,", 1),
			}),
			wantErr: "CircuitHousing derives from",
		},
		{
			name: "a chain that no longer reaches Device",
			files: tree(map[string]string{
				smallDevicePath: strings.Replace(smallDevice, ": Device", ": Thing", 1),
			}),
			wantErr: "SmallDevice derives from",
		},
		{
			name: "an accessor moved onto an intermediate class",
			files: tree(map[string]string{
				logicUnitBasePath: strings.Replace(logicUnitBase, "public virtual double Setting",
					"public virtual bool CanLogicRead(LogicType logicType)", 1),
			}),
			wantErr: "LogicUnitBase declares",
		},
		{
			name:    "an absent intermediate class",
			files:   map[string]string{housingPath: housing},
			wantErr: "not found",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkChain(writeTree(t, test.files), housingChain, housingAccessors)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("checkChain: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("checkChain error = %v, want one containing %q", err, test.wantErr)
			}
		})
	}
}
