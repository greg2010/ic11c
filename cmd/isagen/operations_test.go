package main

import (
	"slices"
	"strings"
	"testing"
)

// chipSource wraps the members a case declares in the type they are read out
// of, so a case states only the classes it is about.
func chipSource(members string) string {
	return "namespace Assets.Scripts.Objects.Electrical;\n\npublic class ProgrammableChip\n{\n" + members + "\n}\n"
}

// The root of the hierarchy and the two shapes above it that hold a store,
// written as the decompiler writes them. Cases that need them concatenate them
// rather than restating them.
const (
	operationRoot = `
	private abstract class _Operation
	{
		public _Operation(ProgrammableChip chip, int lineNumber)
		{
			_Chip = chip;
		}
	}
`
	operationStore = `
	private abstract class _Operation_1_0 : _Operation
	{
		protected IndexVariable _Store;

		public _Operation_1_0(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber)
		{
			_Store = new IndexVariable(chip, lineNumber, registerStoreCode, InstructionInclude.MaskStoreIndex, throwException: false);
		}
	}

	private abstract class _Operation_1_1 : _Operation_1_0
	{
		protected DoubleValueVariable _Argument1;

		public _Operation_1_1(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode)
		{
			_Argument1 = new DoubleValueVariable(chip, lineNumber, argument1Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}
	}
`
)

// lineOfCode wraps a switch body in the constructor the operations are built
// from.
func lineOfCode(arms string) string {
	return `
	private class _LineOfCode
	{
		public _LineOfCode(ProgrammableChip chip, string lineOfCode, int lineNumber)
		{
			string[] array = lineOfCode.Split();
			switch ((ScriptCommand)Enum.Parse(typeof(ScriptCommand), array[0]))
			{
` + arms + `
			}
		}
	}
`
}

func TestParseOperandUses(t *testing.T) {
	tests := []struct {
		name    string
		members string
		// want is the direction of each operand, and wantUndetermined a
		// fragment of the explanation when the reading does not finish.
		want             []Direction
		wantUndetermined string
	}{
		{
			name: "the store is the first operand",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _MOVE_Operation : _Operation_1_1
	{
		public _MOVE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Argument1.GetVariableValue(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionWrite, DirectionRead},
		},
		{
			name: "the store is not the first operand",
			members: lineOfCode(`
			case ScriptCommand.swap:
				Operation = new _SWAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SWAP_Operation : _Operation_1_1
	{
		public _SWAP_Operation(ProgrammableChip chip, int lineNumber, string argument1Code, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Argument1.GetVariableValue(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionRead, DirectionWrite},
		},
		{
			name: "an operand spelled like a store that nothing writes",
			members: lineOfCode(`
			case ScriptCommand.s:
				Operation = new _S_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + `
	private class _S_Operation : _Operation
	{
		protected readonly DoubleValueVariable _Argument1;

		public _S_Operation(ProgrammableChip chip, int lineNumber, string deviceCode, string registerOrValueCode)
			: base(chip, lineNumber)
		{
			_Argument1 = new DoubleValueVariable(chip, lineNumber, registerOrValueCode, InstructionInclude.MaskDoubleValue, throwException: false);
		}

		public override int Execute(int index)
		{
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionRead, DirectionRead},
		},
		{
			name: "a shorthand that hands its long form a literal",
			members: lineOfCode(`
			case ScriptCommand.seqz:
				Operation = new _SEQZ_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SEQ_Operation : _Operation_1_1
	{
		protected DoubleValueVariable _Argument2;

		public _SEQ_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
			_Argument2 = new DoubleValueVariable(chip, lineNumber, argument2Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 1.0;
			return index + 1;
		}
	}

	private class _SEQZ_Operation : _SEQ_Operation
	{
		public _SEQZ_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, "0")
		{
		}
	}
`,
			want: []Direction{DirectionWrite, DirectionRead},
		},
		{
			name: "the write is declared by a class further up",
			members: lineOfCode(`
			case ScriptCommand.pop:
				Operation = new _POP_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _PEEK_Operation : _Operation_1_0
	{
		public _PEEK_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip._Stack[0];
			return index + 1;
		}
	}

	private class _POP_Operation : _PEEK_Operation
	{
		public _POP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}
	}
`,
			want: []Direction{DirectionWrite},
		},
		{
			name: "the registers written are the fixed ones only",
			members: lineOfCode(`
			case ScriptCommand.jal:
				Operation = new _JAL_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + `
	private class _JAL_Operation : _Operation
	{
		public _JAL_Operation(ProgrammableChip chip, int lineNumber, string jumpAddressCode)
			: base(chip, lineNumber)
		{
		}

		public override int Execute(int index)
		{
			_Chip._Registers[_Chip._ReturnAddressIndex] = index + 1;
			_Chip._Registers[_Chip._StackPointerIndex] += 1.0;
			return index;
		}
	}
`,
			want: []Direction{DirectionRead},
		},
		{
			name: "two operands are written",
			members: lineOfCode(`
			case ScriptCommand.divmod:
				Operation = new _DIVMOD_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + `
	private class _DIVMOD_Operation : _Operation_1_1
	{
		protected IndexVariable _Remainder;

		public _DIVMOD_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string remainderCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
			_Remainder = new IndexVariable(chip, lineNumber, remainderCode, InstructionInclude.MaskStoreIndex, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			int variableIndex2 = _Remainder.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 0.0;
			_Chip._Registers[variableIndex2] = 0.0;
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionWrite, DirectionWrite, DirectionRead},
		},
		{
			name: "the register written is named by nothing the reader knows",
			members: lineOfCode(`
			case ScriptCommand.scatter:
				Operation = new _SCATTER_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + `
	private class _SCATTER_Operation : _Operation
	{
		public _SCATTER_Operation(ProgrammableChip chip, int lineNumber, string argument1Code)
			: base(chip, lineNumber)
		{
		}

		public override int Execute(int index)
		{
			_Chip._Registers[_Chip._Selected] = 1.0;
			return index + 1;
		}
	}
`,
			wantUndetermined: "names no operand's variable",
		},
		{
			name: "the register written is folded into rather than replaced",
			members: lineOfCode(`
			case ScriptCommand.incr:
				Operation = new _INCR_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INCR_Operation : _Operation_1_0
	{
		public _INCR_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] += 1.0;
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite},
		},
		{
			// The shape ins is written in. A reading that classified a mention
			// by the statement it sits in would see the store alone and call
			// the operand a plain destination, which is a value the allocator
			// would then believe dead across the instruction.
			name: "the register read and written by separate statements",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _INS_Operation : _Operation_1_1
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long num = DoubleToLong(_Chip._Registers[variableIndex], signed: false) & 0xFL;
			_Chip._Registers[variableIndex] = LongToDouble(num | (long)_Argument1.GetVariableValue(_AliasTarget.Register));
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite, DirectionRead},
		},
		{
			name: "the register written is stepped rather than replaced",
			members: lineOfCode(`
			case ScriptCommand.incr:
				Operation = new _INCR_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INCR_Operation : _Operation_1_0
	{
		public _INCR_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex]++;
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite},
		},
		{
			name: "the register written is stepped down rather than replaced",
			members: lineOfCode(`
			case ScriptCommand.decr:
				Operation = new _DECR_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _DECR_Operation : _Operation_1_0
	{
		public _DECR_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex]--;
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite},
		},
		{
			// The operator says what the mention does to the entry, so one this
			// reader has no entry for leaves the mention unclassified. Reading it
			// as the direction a mention with no operator after it gets is the
			// guess that hides a store.
			name: "the register is reached through an operator the reader does not know",
			members: lineOfCode(`
			case ScriptCommand.peek:
				Operation = new _PEEK_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _PEEK_Operation : _Operation_1_0
	{
		public _PEEK_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double num = _Chip._Registers[variableIndex]!.CompareTo(0.0);
			return index + 1;
		}
	}
`,
			wantUndetermined: `follows a register file mention with "!"`,
		},
		{
			// Every comparison operator ends in the byte a compound assignment
			// ends in, so a reader that only asked whether the operator assigns
			// would call each of these a fold and declare the register written.
			name: "the register is compared rather than assigned",
			members: lineOfCode(`
			case ScriptCommand.bdns:
				Operation = new _BDNS_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _BDNS_Operation : _Operation_1_1
	{
		public _BDNS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			if (_Chip._Registers[variableIndex] == 0.0 || _Chip._Registers[variableIndex] != 1.0 || _Chip._Registers[variableIndex] >= 2.0 || _Chip._Registers[variableIndex] <= 3.0)
			{
				return (int)_Argument1.GetVariableValue(_AliasTarget.Register);
			}
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionRead, DirectionRead},
		},
		{
			// The classes _Operation nests to resolve an operand's code are
			// skipped, and only that skip keeps every instruction from reading as
			// undetermined. It is bounded by the name those classes share, so a
			// container holding a store rather than a resolution is reported.
			name: "the register is reached from a nested type that is not an operand variable",
			members: lineOfCode(`
			case ScriptCommand.poke:
				Operation = new _POKE_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _POKE_Operation : _Operation_1_0
	{
		private class Writeback
		{
			public void Commit(int variableIndex)
			{
				_Chip._Registers[variableIndex] = 1.0;
			}
		}

		public _POKE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			wantUndetermined: "nests Writeback, which reaches the register file",
		},
		{
			name: "the variable written is built where the reader cannot see it",
			members: lineOfCode(`
			case ScriptCommand.pick:
				Operation = new _PICK_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + `
	private class _PICK_Operation : _Operation
	{
		private readonly IndexVariable _Target;

		public _PICK_Operation(ProgrammableChip chip, int lineNumber, string targetCode)
			: base(chip, lineNumber)
		{
			if (targetCode[0] == 'r')
			{
				_Target = new IndexVariable(chip, lineNumber, targetCode, InstructionInclude.MaskStoreIndex, throwException: false);
			}
		}

		public override int Execute(int index)
		{
			int variableIndex = _Target.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 1.0;
			return index + 1;
		}
	}
`,
			wantUndetermined: "which no constructor binds to an operand",
		},
		{
			// One variable, two operands, and no answer that is not a guess at
			// which one it stands for. The reading is discarded rather than
			// settled: settling it would also make the direction depend on which
			// parameter the search reached last.
			name: "the variable written is built from two operands at once",
			members: lineOfCode(`
			case ScriptCommand.pick:
				Operation = new _PICK_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + `
	private class _PICK_Operation : _Operation
	{
		private readonly IndexVariable _Target;

		public _PICK_Operation(ProgrammableChip chip, int lineNumber, string firstCode, string secondCode)
			: base(chip, lineNumber)
		{
			_Target = new IndexVariable(chip, lineNumber, firstCode + secondCode, InstructionInclude.MaskStoreIndex, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Target.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 1.0;
			return index + 1;
		}
	}
`,
			wantUndetermined: "binds _Target from more than one operand",
		},
		{
			name: "the operation is built with more arguments than it takes",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + `
	private class _MOVE_Operation : _Operation_1_1
	{
		public _MOVE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}
	}
`,
			wantUndetermined: "takes 4 constructor arguments but is built with 5",
		},
		{
			name: "the operation extends a class the source does not declare",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1]);
				break;
`) + `
	private class _MOVE_Operation : _Operation_9_9
	{
		public _MOVE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}
	}
`,
			wantUndetermined: "which ProgrammableChip does not declare",
		},
		{
			name: "the operation declares no constructor",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + `
	private class _MOVE_Operation : _Operation
	{
		public override int Execute(int index)
		{
			return index + 1;
		}
	}
`,
			wantUndetermined: "declares no constructor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uses, err := parseOperandUses(chipSource(tt.members))
			if err != nil {
				t.Fatalf("parseOperandUses: %v", err)
			}
			if len(uses) != 1 {
				t.Fatalf("parseOperandUses covered %d mnemonics, want 1", len(uses))
			}
			var got operandUses
			for _, only := range uses {
				got = only
			}
			if tt.wantUndetermined != "" {
				if got.undetermined == "" {
					t.Fatalf("uses = %v, want it undetermined mentioning %q", got.uses, tt.wantUndetermined)
				}
				if !strings.Contains(got.undetermined, tt.wantUndetermined) {
					t.Errorf("undetermined = %q, want it to mention %q", got.undetermined, tt.wantUndetermined)
				}
				return
			}
			if got.undetermined != "" {
				t.Fatalf("uses are undetermined: %s", got.undetermined)
			}
			for position := range got.uses {
				if position >= len(tt.want) {
					t.Errorf("operand %d is reached, but the case builds only %d operands", position, len(tt.want))
				}
			}
			directions := make([]Direction, len(tt.want))
			for i := range directions {
				directions[i] = got.direction(i)
			}
			if !slices.Equal(directions, tt.want) {
				t.Errorf("directions = %v, want %v", directions, tt.want)
			}
		})
	}
}

// TestRegisterFileOperators covers the classification the whole reading of a
// direction rests on. Each case is the text a member carries just past the
// closing bracket of a register file mention.
func TestRegisterFileOperators(t *testing.T) {
	tests := []struct {
		name string
		tail string
		want registerUse
		// wantUnknown is an operator with no classification, which stops the
		// reading rather than being taken for the direction a mention nothing
		// follows would get.
		wantUnknown bool
	}{
		{name: "the mention ends the statement", tail: ";", want: useRead},
		{name: "the mention ends the text", tail: "", want: useRead},
		{name: "the mention is an argument", tail: ", signed: false)", want: useRead},
		{name: "the mention is a receiver", tail: ".CompareTo(0.0)", want: useRead},
		{name: "the entry is replaced", tail: " = 1.0;", want: useWrite},
		{name: "the entry is stepped up", tail: "++;", want: useRead | useWrite},
		{name: "the entry is stepped down", tail: "--;", want: useRead | useWrite},
		{name: "a value is added into the entry", tail: " += 1.0;", want: useRead | useWrite},
		{name: "the entry is shifted in place", tail: " <<= 1;", want: useRead | useWrite},
		{name: "the entry is tested equal", tail: " == 0.0)", want: useRead},
		{name: "the entry is tested unequal", tail: " != 0.0)", want: useRead},
		{name: "the entry is tested not below", tail: " >= 0.0)", want: useRead},
		{name: "the entry is tested not above", tail: " <= 0.0)", want: useRead},
		{name: "the entry is tested below", tail: " < 0.0)", want: useRead},
		{name: "the entry is added to", tail: " + 1.0;", want: useRead},
		{name: "the entry chooses a value", tail: " ? 1.0 : 0.0;", want: useRead},
		{name: "the entry is marked non-null", tail: "!.CompareTo(0.0)", wantUnknown: true},
		{name: "the entry heads a lambda", tail: " => 1.0;", wantUnknown: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operator := operatorAfter(tt.tail, 0)
			got, known := operatorUses[operator]
			if known == tt.wantUnknown {
				t.Fatalf("operator %q is classified = %t, want %t", operator, known, !tt.wantUnknown)
			}
			if !tt.wantUnknown && got != tt.want {
				t.Errorf("operator %q reaches the entry as %d, want %d", operator, got, tt.want)
			}
		})
	}
}

func TestParseOperandUsesErrors(t *testing.T) {
	tests := []struct {
		name    string
		members string
		wantErr string
	}{
		{
			name:    "no class turns a line into an operation",
			members: operationRoot,
			wantErr: "class _LineOfCode",
		},
		{
			name: "the class that does declares no constructor",
			members: operationRoot + `
	private class _LineOfCode
	{
		public readonly _Operation Operation;
	}
`,
			wantErr: "_LineOfCode constructor",
		},
		{
			name: "no operation classes at all",
			members: `
	private class _LineOfCode
	{
		public _LineOfCode(ProgrammableChip chip, string lineOfCode, int lineNumber)
		{
		}
	}
`,
			wantErr: "switch building the operations",
		},
		{
			name: "a mnemonic building two operations",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1]);
				break;
			case ScriptCommand.move:
				Operation = new _NOOP_Operation(chip, lineNumber);
				break;
`) + operationRoot,
			wantErr: "builds both",
		},
		{
			name: "a case label building nothing",
			members: lineOfCode(`
			case ScriptCommand.move:
				break;
`) + operationRoot,
			wantErr: "build no operation",
		},
		{
			// An index this program reads as no operand at all leaves the
			// argument's direction at the default, which reports an operand the
			// instruction writes as one it only reads.
			name: "an operand index too large to read",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[99999999999999999999]);
				break;
`) + operationRoot,
			wantErr: "operand index in array[99999999999999999999]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseOperandUses(chipSource(tt.members))
			checkErr(t, "parseOperandUses", err, tt.wantErr)
		})
	}
}
