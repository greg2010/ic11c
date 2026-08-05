package main

import (
	"slices"
	"strings"
	"testing"
)

// chipSource wraps a case's members in the type they are read out of.
func chipSource(members string) string {
	return "namespace Assets.Scripts.Objects.Electrical;\n\npublic class ProgrammableChip\n{\n" + members + "\n}\n"
}

// The hierarchy root and the two shapes above it that hold a store, written as
// the decompiler writes them. Cases concatenate rather than restate them.
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
	operationArgument2 = `
	private abstract class _Operation_1_2 : _Operation_1_1
	{
		protected DoubleValueVariable _Argument2;

		public _Operation_1_2(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
			_Argument2 = new DoubleValueVariable(chip, lineNumber, argument2Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}
	}
`
)

// onlyOperandUses reads the one mnemonic a case declares.
func onlyOperandUses(t *testing.T, members string) operandUses {
	t.Helper()
	uses, err := parseOperandUses(chipSource(members))
	if err != nil {
		t.Fatalf("parseOperandUses: %v", err)
	}
	if len(uses) != 1 {
		t.Fatalf("parseOperandUses covered %d mnemonics, want 1", len(uses))
	}
	for _, only := range uses {
		return only
	}
	return operandUses{}
}

// lineOfCode wraps a switch body in the constructor the operations are built from.
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
		want    []Direction
		// wantImplicit is what the operation does to the registers no operand
		// names. Every case states it, so one that gains a use of a fixed
		// register fails rather than passing on the operands alone.
		wantImplicit []ImplicitUse
		// wantUndetermined is a fragment of the explanation the reading stops with.
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
			wantImplicit: []ImplicitUse{
				{Register: "ra", Direction: DirectionWrite},
				{Register: "sp", Direction: DirectionReadWrite},
			},
		},
		{
			// The shape the eighteen conditional linking jumps are written in.
			name: "the return address is written only where the jump is taken",
			members: lineOfCode(`
			case ScriptCommand.beqal:
				Operation = new _BEQAL_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + `
	private class _BEQAL_Operation : _Operation
	{
		public _BEQAL_Operation(ProgrammableChip chip, int lineNumber, string jumpAddressCode)
			: base(chip, lineNumber)
		{
		}

		public override int Execute(int index)
		{
			if (index > 0)
			{
				_Chip._Registers[_Chip._ReturnAddressIndex] = index + 1;
				return index;
			}
			return index + 1;
		}
	}
`,
			want:         []Direction{DirectionRead},
			wantImplicit: []ImplicitUse{{Register: "ra", Direction: DirectionReadWrite}},
		},
		{
			// An operand destination is stated a second time by the operand list,
			// which checkDirections holds this reading to. Nothing states a fixed
			// register twice, which is why only those are read this way.
			name: "an operand destination written only in a branch is still written",
			members: lineOfCode(`
			case ScriptCommand.lr:
				Operation = new _LR_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _LR_Operation : _Operation_1_1
	{
		public _LR_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			if (variableValue > 0.0)
			{
				_Chip._Registers[variableIndex] = variableValue;
			}
			return index + 1;
		}
	}
`,
			want:         []Direction{DirectionWrite, DirectionRead},
			wantImplicit: []ImplicitUse{},
		},
		{
			name: "the nested type is one of the operand variable classes",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + `
	private abstract class _Operation
	{
		public _Operation(ProgrammableChip chip, int lineNumber)
		{
			_Chip = chip;
		}

		public class IndexVariable
		{
			public int GetVariableIndex(_AliasTarget target)
			{
				return (int)_Chip._Registers[0];
			}
		}
	}
` + operationStore + `
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
			name: "a local naming a register is resolved a second time",
			members: lineOfCode(`
			case ScriptCommand.divmod:
				Operation = new _DIVMOD_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _DIVMOD_Operation : _Operation_1_0
	{
		protected IndexVariable _Remainder;

		public _DIVMOD_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string remainderCode)
			: base(chip, lineNumber, registerStoreCode)
		{
			_Remainder = new IndexVariable(chip, lineNumber, remainderCode, InstructionInclude.MaskStoreIndex, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 0.0;
			variableIndex = _Remainder.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 1.0;
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionWrite, DirectionWrite},
		},
		{
			name: "a local naming a register is assigned from something else",
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
			variableIndex = 3;
			_Chip._Registers[variableIndex] = _Argument1.GetVariableValue(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			wantUndetermined: "variableIndex",
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
			// The shape ins is written in. A reading keyed on the statement a
			// mention sits in would see the store alone and call the operand a
			// plain destination, which the allocator then believes dead.
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
			_Chip._Registers[variableIndex] = LongToDouble(num | _Argument1.GetVariableLong(_AliasTarget.Register));
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
			name: "the register written is stepped by a prefix",
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
			++_Chip._Registers[variableIndex];
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite},
		},
		{
			name: "the register written is stepped down by a prefix",
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
			--_Chip._Registers[variableIndex];
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite},
		},
		{
			name: "a step on something else ahead of the mention only reads it",
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
			int num = index---_Chip._Registers[variableIndex];
			return num;
		}
	}
`,
			want: []Direction{DirectionRead},
		},
		{
			name: "the register is passed by reference",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _MOVE_Operation : _Operation_1_0
	{
		public _MOVE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip.Fold(ref _Chip._Registers[variableIndex]);
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionReadWrite},
		},
		{
			name: "the register is passed as an out parameter",
			members: lineOfCode(`
			case ScriptCommand.move:
				Operation = new _MOVE_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _MOVE_Operation : _Operation_1_0
	{
		public _MOVE_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip.Fetch(out _Chip._Registers[variableIndex]);
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionWrite},
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
			// ends in.
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
				return _Argument1.GetVariableValue(_AliasTarget.Register);
			}
			return index + 1;
		}
	}
`,
			want: []Direction{DirectionRead, DirectionRead},
		},
		{
			// The skip for the classes _Operation nests to resolve an operand's
			// code is bounded by the name they share, so a container holding a
			// store rather than a resolution is reported instead.
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
			public void Commit(ProgrammableChip chip, int variableIndex)
			{
				chip._Registers[variableIndex] = 1.0;
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
			wantUndetermined: "nests Writeback",
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
			got := onlyOperandUses(t, tt.members)
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
			if implicit := got.implicitUses(); !slices.Equal(implicit, tt.wantImplicit) {
				t.Errorf("implicit uses = %v, want %v", implicit, tt.wantImplicit)
			}
		})
	}
}

// TestParseOperandConversions covers the conversion each operand's value is read
// through, which is per operand rather than per instruction: the shifts read a
// value one way and a distance another, and sra and srl differ only in the sign.
func TestParseOperandConversions(t *testing.T) {
	tests := []struct {
		name    string
		members string
		want    []Conversion
		// wantUndetermined is a fragment of the explanation the reading stops with.
		wantUndetermined string
	}{
		{
			// sra: the value reduces with its sign, the distance faults at a
			// bound two orders further in.
			name: "the shift reads its value signed and its distance as an int",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _SRA_Operation : _Operation_1_2
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, argument2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register);
			int variableInt = _Argument2.GetVariableInt(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong >> variableInt);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionSignedLong, ConversionInt},
		},
		{
			// srl differs from sra in one named argument and nowhere else.
			name: "the shift reads its value unsigned",
			members: lineOfCode(`
			case ScriptCommand.srl:
				Operation = new _SRL_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _SRL_Operation : _Operation_1_2
	{
		public _SRL_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, argument2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false);
			int variableInt = _Argument2.GetVariableInt(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong >> variableInt);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionUnsignedLong, ConversionInt},
		},
		{
			name: "the rotate reads its distance through an expression",
			members: lineOfCode(`
			case ScriptCommand.rol:
				Operation = new _ROL_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _ROL_Operation : _Operation_1_2
	{
		public _ROL_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, argument2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long num = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false) & 0x3FFFFFFFFFFFFFL;
			int num2 = (_Argument2.GetVariableInt(_AliasTarget.Register) % 54 + 54) % 54;
			_Chip._Registers[variableIndex] = LongToDouble((num << num2) | (num >> 54 - num2));
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionUnsignedLong, ConversionInt},
		},
		{
			// ext: one value and two field bounds.
			name: "the field instruction reads two bounds as ints",
			members: lineOfCode(`
			case ScriptCommand.ext:
				Operation = new _EXT_Operation(chip, lineNumber, array[1], array[2], array[3], array[4]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _EXT_Operation : _Operation_1_2
	{
		protected DoubleValueVariable _Argument3;

		public _EXT_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string arg1Code, string arg2Code, string arg3Code)
			: base(chip, lineNumber, registerStoreCode, arg1Code, arg2Code)
		{
			_Argument3 = new DoubleValueVariable(chip, lineNumber, arg3Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false);
			int variableInt = _Argument2.GetVariableInt(_AliasTarget.Register);
			int variableInt2 = _Argument3.GetVariableInt(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong >> variableInt << variableInt2);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionUnsignedLong, ConversionInt, ConversionInt},
		},
		{
			// ins folds into its destination, reading what was there through the
			// reduction directly rather than through the operand's variable.
			name: "the destination is converted where the instruction folds into it",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _INS_Operation : _Operation_1_2
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string arg1Code, string arg2Code)
			: base(chip, lineNumber, registerStoreCode, arg1Code, arg2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false);
			int variableInt = _Argument2.GetVariableInt(_AliasTarget.Register);
			long num = DoubleToLong(_Chip._Registers[variableIndex], signed: false) & 0x1FFFFFFFFFFFFFL;
			_Chip._Registers[variableIndex] = LongToDouble(num | (variableLong << variableInt));
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionUnsignedLong, ConversionUnsignedLong, ConversionInt},
		},
		{
			name: "the bitwise form reads both operands signed",
			members: lineOfCode(`
			case ScriptCommand.and:
				Operation = new _AND_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _AND_Operation : _Operation_1_2
	{
		public _AND_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, argument2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register);
			long variableLong2 = _Argument2.GetVariableLong(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong & variableLong2);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionSignedLong, ConversionSignedLong},
		},
		{
			name: "an operand read as a plain double is converted by nothing",
			members: lineOfCode(`
			case ScriptCommand.add:
				Operation = new _ADD_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _ADD_Operation : _Operation_1_2
	{
		public _ADD_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, argument2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			double variableValue2 = _Argument2.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = variableValue + variableValue2;
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone, ConversionNone},
		},
		{
			name: "the operand is read through a call the reader has no classification for",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Argument1.GetVariableFixedPoint(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			wantUndetermined: "GetVariableFixedPoint",
		},
		{
			name: "the sign of the reduction is passed positionally",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(_Argument1.GetVariableLong(_AliasTarget.Register, false));
			return index + 1;
		}
	}
`,
			wantUndetermined: "which names no parameter and no type",
		},
		{
			name: "the register file entry is read through a call that converts nothing this knows",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INS_Operation : _Operation_1_0
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(_Chip._Registers[variableIndex]);
			return index + 1;
		}
	}
`,
			wantUndetermined: "LongToDouble",
		},
		{
			name: "the register file entry is read through a cast",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INS_Operation : _Operation_1_0
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long num = (long)_Chip._Registers[variableIndex];
			_Chip._Registers[variableIndex] = LongToDouble(num);
			return index + 1;
		}
	}
`,
			wantUndetermined: "says nothing about what converts",
		},
		{
			name: "the operand's value is read through a cast",
			members: lineOfCode(`
			case ScriptCommand.mul:
				Operation = new _MUL_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _MUL_Operation : _Operation_1_1
	{
		public _MUL_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableValue = (long)_Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableValue);
			return index + 1;
		}
	}
`,
			wantUndetermined: "says nothing about what converts",
		},
		{
			name: "the operand's value is handed to a call with no classification",
			members: lineOfCode(`
			case ScriptCommand.mul:
				Operation = new _MUL_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _MUL_Operation : _Operation_1_1
	{
		public _MUL_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			int variableValue = (int)Math.Round(_Argument1.GetVariableValue(_AliasTarget.Register));
			_Chip._Registers[variableIndex] = variableValue;
			return index + 1;
		}
	}
`,
			wantUndetermined: "Round",
		},
		{
			// rmap: the reduction is written at the local rather than at the read.
			name: "the body casts the double it read to an int",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash((int)variableValue);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNarrowedInt},
		},
		{
			// ins: the same cast written over an integer a reader already produced.
			name: "the body casts an integer a reader produced",
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
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false);
			ulong num = (ulong)variableLong & 9007199254740991uL;
			_Chip._Registers[variableIndex] = LongToDouble((long)num);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionUnsignedLong},
		},
		{
			name: "the body casts a value it read to a type with no reading",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = (short)variableValue;
			return index + 1;
		}
	}
`,
			wantUndetermined: `from "double" to "short"`,
		},
		{
			name: "the body casts a value bound without a declared type",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue;
			variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = (int)variableValue;
			return index + 1;
		}
	}
`,
			wantUndetermined: `from "" to "int"`,
		},
		{
			name: "a keyword's clause ahead of a bound local is not a cast",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			if (index > 0) variableValue.ToString();
			_Chip._Registers[variableIndex] = variableValue;
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone},
		},
		{
			name: "the operand's value is handed back and tested",
			members: lineOfCode(`
			case ScriptCommand.j:
				Operation = new _J_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _J_Operation : _Operation_1_1
	{
		public _J_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			if (double.IsNaN(_Argument1.GetVariableValue(_AliasTarget.Register)))
			{
				return index + 1;
			}
			return _Argument1.GetVariableValue(_AliasTarget.Register);
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone},
		},
		{
			name: "one operand is read through two conversions",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register);
			int variableInt = _Argument1.GetVariableInt(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong >> variableInt);
			return index + 1;
		}
	}
`,
			wantUndetermined: "through both",
		},
		{
			// The zero-comparing shorthands read a variable their long form built
			// from a literal, which stands for no operand at all.
			name: "a shorthand reads the variable its long form built from a literal",
			members: lineOfCode(`
			case ScriptCommand.seqz:
				Operation = new _SEQZ_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + operationArgument2 + `
	private class _SEQ_Operation : _Operation_1_2
	{
		public _SEQ_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code, argument2Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			double variableValue2 = _Argument2.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = ((variableValue == variableValue2) ? 1.0 : 0.0);
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
			want: []Conversion{ConversionNone, ConversionNone},
		},
		{
			// alias builds its target inside a branch, so nothing says which
			// operand the variable stands for.
			name: "an operand built inside a branch is resolved rather than converted",
			members: lineOfCode(`
			case ScriptCommand.alias:
				Operation = new _ALIAS_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + `
	private class _ALIAS_Operation : _Operation
	{
		private readonly IndexVariable _Target;

		public _ALIAS_Operation(ProgrammableChip chip, int lineNumber, string aliasCode, string targetCode)
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
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone},
		},
		{
			name: "an unclassified reader of an operand's value is refused wherever it is called",
			members: lineOfCode(`
			case ScriptCommand.alias:
				Operation = new _ALIAS_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + `
	private class _ALIAS_Operation : _Operation
	{
		private readonly IndexVariable _Target;

		public _ALIAS_Operation(ProgrammableChip chip, int lineNumber, string aliasCode, string targetCode)
			: base(chip, lineNumber)
		{
			if (targetCode[0] == 'r')
			{
				_Target = new IndexVariable(chip, lineNumber, targetCode, InstructionInclude.MaskStoreIndex, throwException: false);
			}
		}

		public override int Execute(int index)
		{
			int variableIndex = _Target.GetVariableFixedPoint(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			wantUndetermined: "GetVariableFixedPoint",
		},
		{
			name: "the operand is read from a nested type that is not an operand variable",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		private class Distance
		{
			public int Read(DoubleValueVariable v)
			{
				return v.GetVariableInt(_AliasTarget.Register);
			}
		}

		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 0.0;
			return index + 1;
		}
	}
`,
			wantUndetermined: "nests Distance",
		},
		{
			name: "the operand read is built where the reader cannot see it",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_0
	{
		private readonly DoubleValueVariable _Distance;

		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string distanceCode)
			: base(chip, lineNumber, registerStoreCode)
		{
			if (distanceCode[0] == 'r')
			{
				_Distance = new DoubleValueVariable(chip, lineNumber, distanceCode, InstructionInclude.MaskDoubleValue, throwException: false);
			}
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Distance.GetVariableInt(_AliasTarget.Register);
			return index + 1;
		}
	}
`,
			wantUndetermined: "binds _Distance to an operand",
		},
		{
			name: "the body casts a call it handed the value to",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash((int)Math.Round(variableValue));
			return index + 1;
		}
	}
`,
			wantUndetermined: `from "" to "int"`,
		},
		{
			name: "the body casts a parenthesized value",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash((int)(variableValue));
			return index + 1;
		}
	}
`,
			wantUndetermined: "parenthesized",
		},
		{
			name: "the body hands the value to a call with no classification",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash(Convert.ToInt32(variableValue));
			return index + 1;
		}
	}
`,
			wantUndetermined: "ToInt32",
		},
		{
			name: "the entry is reduced as an argument that is not the first",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INS_Operation : _Operation_1_0
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long num = DoubleToLong(signed: false, value: _Chip._Registers[variableIndex]);
			_Chip._Registers[variableIndex] = LongToDouble(num);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionUnsignedLong},
		},
		{
			name: "the entry is handed to an unclassified call after a separator",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INS_Operation : _Operation_1_0
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long num = Clamp(0L, _Chip._Registers[variableIndex]);
			_Chip._Registers[variableIndex] = LongToDouble(num);
			return index + 1;
		}
	}
`,
			wantUndetermined: "Clamp",
		},
		{
			name: "the entry reaches a reducing call as part of a larger expression",
			members: lineOfCode(`
			case ScriptCommand.ins:
				Operation = new _INS_Operation(chip, lineNumber, array[1]);
				break;
`) + operationRoot + operationStore + `
	private class _INS_Operation : _Operation_1_0
	{
		public _INS_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode)
			: base(chip, lineNumber, registerStoreCode)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long num = DoubleToLong(_Chip._Registers[variableIndex] + 1.0, signed: false);
			_Chip._Registers[variableIndex] = LongToDouble(num);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone},
		},
		{
			name: "the value is cast as part of a larger expression",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble((long)(variableLong + 1L));
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionSignedLong},
		},
		{
			name: "the value is returned",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			int variableInt = _Argument1.GetVariableInt(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 0.0;
			return variableInt;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionInt},
		},
		{
			name: "the value stands alone in a braceless statement",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			if (index > 0)
				_Chip._Registers[variableIndex] = 0.0;
			else
				variableValue.ToString();
			do
				variableValue.ToString();
			while (index < 0);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone},
		},
		{
			name: "the value is cast through a name that is not one identifier",
			members: lineOfCode(`
			case ScriptCommand.sra:
				Operation = new _SRA_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _SRA_Operation : _Operation_1_1
	{
		public _SRA_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			int variableInt = _Argument1.GetVariableInt(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 0.0;
			throw new ProgrammableChipException((ProgrammableChipException.ICExceptionType)variableInt, _LineNumber);
		}
	}
`,
			wantUndetermined: "not a plain type name",
		},
		{
			name: "the value is assigned over before it is cast",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			variableValue = 1.0;
			_Chip._Registers[variableIndex] = LongToDouble((int)variableValue);
			return index + 1;
		}
	}
`,
			wantUndetermined: "variableValue",
		},
		{
			name: "a class rebuilds the variable its base built",
			members: lineOfCode(`
			case ScriptCommand.srl:
				Operation = new _SRL_Operation(chip, lineNumber, array[1], array[2], array[3]);
				break;
`) + operationRoot + operationStore + `
	private class _SRL_Operation : _Operation_1_1
	{
		public _SRL_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code, string argument2Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
			_Argument1 = new DoubleValueVariable(chip, lineNumber, argument2Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone, ConversionUnsignedLong},
		},
		{
			// The branch forms resolve their device inline, so the call taking the
			// value has a parenthesized receiver whose closing parenthesis stands
			// exactly where a cast written over the call would.
			name: "the value is handed to a call whose receiver is parenthesized",
			members: lineOfCode(`
			case ScriptCommand.bdnvl:
				Operation = new _BDNVL_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _BDNVL_Operation : _Operation_1_1
	{
		public _BDNVL_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			if (!(_Chip.CircuitHousing.GetLogicableFromIndex(0) ?? throw new ProgrammableChipException(ProgrammableChipException.ICExceptionType.DeviceNotFound, _LineNumber)).CanLogicRead(variableValue))
			{
				_Chip._Registers[variableIndex] = 0.0;
			}
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNone},
		},
		{
			name: "the converted variable is built from more than one operand",
			members: lineOfCode(`
			case ScriptCommand.srl:
				Operation = new _SRL_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + `
	private class _SRL_Operation : _Operation
	{
		protected DoubleValueVariable _Target;

		public _SRL_Operation(ProgrammableChip chip, int lineNumber, string argument1Code, string argument2Code)
			: base(chip, lineNumber)
		{
			_Target = new DoubleValueVariable(chip, lineNumber, argument1Code ?? argument2Code, InstructionInclude.MaskDoubleValue, throwException: false);
		}

		public override int Execute(int index)
		{
			long variableLong = _Target.GetVariableLong(_AliasTarget.Register, signed: false);
			return index + 1;
		}
	}
`,
			wantUndetermined: "binds _Target from more than one operand",
		},
		{
			name: "the register file entry is read through a call written on it",
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
			long num = _Chip._Registers[variableIndex].AsUnsignedLong() & 9007199254740991L;
			_Chip._Registers[variableIndex] = LongToDouble(num);
			return index + 1;
		}
	}
`,
			wantUndetermined: "AsUnsignedLong",
		},
		{
			name: "the value read is narrowed through a call written on the local",
			members: lineOfCode(`
			case ScriptCommand.and:
				Operation = new _AND_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _AND_Operation : _Operation_1_1
	{
		public _AND_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong.NarrowTo32());
			return index + 1;
		}
	}
`,
			wantUndetermined: "NarrowTo32",
		},
		{
			name: "the value read is narrowed through a call written on the reader",
			members: lineOfCode(`
			case ScriptCommand.and:
				Operation = new _AND_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _AND_Operation : _Operation_1_1
	{
		public _AND_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register).NarrowTo32();
			_Chip._Registers[variableIndex] = LongToDouble(variableLong);
			return index + 1;
		}
	}
`,
			wantUndetermined: "NarrowTo32",
		},
		{
			name: "the value read is narrowed through the second call of a chain",
			members: lineOfCode(`
			case ScriptCommand.and:
				Operation = new _AND_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _AND_Operation : _Operation_1_1
	{
		public _AND_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register).Find().NarrowTo32();
			_Chip._Registers[variableIndex] = LongToDouble(variableLong);
			return index + 1;
		}
	}
`,
			wantUndetermined: "NarrowTo32",
		},
		{
			name: "the value read is rendered through a call written on the local",
			members: lineOfCode(`
			case ScriptCommand.and:
				Operation = new _AND_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _AND_Operation : _Operation_1_1
	{
		public _AND_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			long variableLong = _Argument1.GetVariableLong(_AliasTarget.Register, signed: false);
			_Chip._Registers[variableIndex] = LongToDouble(variableLong.ToString());
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionUnsignedLong},
		},
		{
			name: "the body checks the cast it narrows through",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash(checked((int)variableValue));
			return index + 1;
		}
	}
`,
			wantUndetermined: "checked",
		},
		{
			// unchecked is the arithmetic every conversion in the table stands
			// for. The source writes neither keyword, so neither is classified.
			name: "the body writes the cast it narrows through unchecked",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash(unchecked((int)variableValue));
			return index + 1;
		}
	}
`,
			wantUndetermined: "unchecked",
		},
		{
			// Nothing in the source reaches this; it is here so that removing the
			// refusal fails.
			name: "an unclassified word stands ahead of the value read",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			throw variableValue;
		}
	}
`,
			wantUndetermined: `puts "throw" ahead of variableValue`,
		},
		{
			// No shape in the source comes near the bound on the walk outward; it
			// is asserted so that widening it fails.
			name: "the value read is enclosed in more calls than the walk follows",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = ` + strings.Repeat("Abs(", 33) + `variableValue` + strings.Repeat(")", 33) + `;
			return index + 1;
		}
	}
`,
			wantUndetermined: "in more than 32 calls",
		},
		{
			name: "the nested type is an enum",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public enum Mode
		{
			A,
			B
		}

		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			double variableValue = _Argument1.GetVariableValue(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = _Chip.CircuitHousing.GetPrefabHashFromReagentHash((int)variableValue);
			return index + 1;
		}
	}
`,
			want: []Conversion{ConversionNone, ConversionNarrowedInt},
		},
		{
			name: "the nested type is a struct",
			members: lineOfCode(`
			case ScriptCommand.rmap:
				Operation = new _RMAP_Operation(chip, lineNumber, array[1], array[2]);
				break;
`) + operationRoot + operationStore + `
	private class _RMAP_Operation : _Operation_1_1
	{
		public struct Mode
		{
			public int Read(DoubleValueVariable v)
			{
				return v.GetVariableInt(_AliasTarget.Register);
			}
		}

		public _RMAP_Operation(ProgrammableChip chip, int lineNumber, string registerStoreCode, string argument1Code)
			: base(chip, lineNumber, registerStoreCode, argument1Code)
		{
		}

		public override int Execute(int index)
		{
			int variableIndex = _Store.GetVariableIndex(_AliasTarget.Register);
			_Chip._Registers[variableIndex] = 0.0;
			return index + 1;
		}
	}
`,
			wantUndetermined: "nests Mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := onlyOperandUses(t, tt.members)
			if tt.wantUndetermined != "" {
				if got.undetermined == "" {
					t.Fatalf("conversions = %v, want it undetermined mentioning %q", got.conversions, tt.wantUndetermined)
				}
				if !strings.Contains(got.undetermined, tt.wantUndetermined) {
					t.Errorf("undetermined = %q, want it to mention %q", got.undetermined, tt.wantUndetermined)
				}
				return
			}
			if got.undetermined != "" {
				t.Fatalf("uses are undetermined: %s", got.undetermined)
			}
			for position := range got.conversions {
				if position >= len(tt.want) {
					t.Errorf("operand %d is converted, but the case builds only %d operands", position, len(tt.want))
				}
			}
			conversions := make([]Conversion, len(tt.want))
			for i := range conversions {
				conversions[i] = got.conversion(i)
			}
			if !slices.Equal(conversions, tt.want) {
				t.Errorf("conversions = %v, want %v", conversions, tt.want)
			}
		})
	}
}

// TestSignedArgument covers the reading every 64-bit reduction's sign rests on,
// case by argument list. The sign is the one fact here no second reading
// cross-checks: an unsigned value recorded signed is wrong only where the top
// bit is set, so a list this cannot attribute in full is refused.
func TestSignedArgument(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		wantSigned bool
		wantNamed  bool
		// wantErr is a fragment of the refusal where the list settles nothing.
		wantErr string
	}{
		{name: "the call names no sign", args: "_AliasTarget.Register", wantSigned: true},
		{name: "the call takes no arguments", args: "", wantSigned: true},
		{name: "the sign is named false", args: "_AliasTarget.Register, signed: false", wantNamed: true},
		{name: "the sign is named true", args: "_AliasTarget.Register, signed: true", wantSigned: true, wantNamed: true},
		{
			name:      "the value is an entry of the register file",
			args:      "_Chip._Registers[variableIndex], signed: false",
			wantNamed: true,
		},
		{
			name:       "another parameter is named",
			args:       "_AliasTarget.Register, errorAtEnd: false",
			wantSigned: true,
		},
		{
			name:    "the sign is a boolean literal passed positionally",
			args:    "_AliasTarget.Register, false",
			wantErr: "false",
		},
		{
			name:    "the sign is a local passed positionally",
			args:    "_AliasTarget.Register, isSigned",
			wantErr: "isSigned",
		},
		{
			name:    "the value is a local passed positionally",
			args:    "variableValue, signed",
			wantErr: "variableValue",
		},
		{
			name:    "the sign is named but is not a literal",
			args:    "_AliasTarget.Register, signed: isSigned",
			wantErr: "isSigned",
		},
		{
			name:    "the sign is named but is parenthesized",
			args:    "_AliasTarget.Register, signed: (false)",
			wantErr: "(false)",
		},
		{
			name:    "the sign is named twice",
			args:    "_AliasTarget.Register, signed: false, signed: true",
			wantErr: "twice",
		},
		{
			name:    "a qualified name is passed positionally",
			args:    "_AliasTarget.Register, ScriptFlags::Unsigned",
			wantErr: "ScriptFlags::Unsigned",
		},
		{
			// The root qualifier the decompiler may write in front of any name.
			name:    "the value carries a root qualifier",
			args:    "global::_AliasTarget.Register, signed: false",
			wantErr: "global::_AliasTarget.Register",
		},
		{
			name:       "the sign is named with no space after the colon",
			args:       "_AliasTarget.Register, signed:false",
			wantNamed:  true,
			wantSigned: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signed, named, err := signedArgument(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("signedArgument(%q) = (%t, %t, nil), want it refused mentioning %q", tt.args, signed, named, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("signedArgument(%q) refused with %q, want it to mention %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("signedArgument(%q): %v", tt.args, err)
			}
			if signed != tt.wantSigned || named != tt.wantNamed {
				t.Errorf("signedArgument(%q) = (%t, %t), want (%t, %t)", tt.args, signed, named, tt.wantSigned, tt.wantNamed)
			}
		})
	}
}

// TestRegisterFileOperators covers the classification the whole reading of a
// direction rests on: the text just past a register file mention's bracket.
func TestRegisterFileOperators(t *testing.T) {
	tests := []struct {
		name string
		tail string
		want registerUse
		// wantUnknown is an operator with no classification, which stops the
		// reading rather than answering as a mention nothing follows would.
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

// TestRegisterFilePrefixes covers the other half: the text just ahead of a
// mention, where the three shapes that write through it are the whole of what
// the operator after the mention cannot see.
func TestRegisterFilePrefixes(t *testing.T) {
	tests := []struct {
		name string
		head string
		want registerUse
		// wantSilent is text that says nothing, leaving the operator after the
		// mention to answer alone.
		wantSilent bool
	}{
		{name: "the mention is stepped up", head: "++", want: useRead | useWrite},
		{name: "the mention is stepped down", head: "--", want: useRead | useWrite},
		{name: "the mention is stepped up with space between", head: "\t\t\t++ ", want: useRead | useWrite},
		{name: "the mention is passed by reference", head: "Fold(ref ", want: useRead | useWrite},
		{name: "the mention is assigned by the callee", head: "Fetch(out ", want: useWrite},
		{name: "the mention opens the text", head: "", wantSilent: true},
		{name: "the mention is assigned", head: "num = ", wantSilent: true},
		{name: "the mention is an argument", head: "LongToDouble(", wantSilent: true},
		{name: "the mention follows a separator", head: "num, ", wantSilent: true},
		{name: "the mention is subtracted from a step", head: "index---", wantSilent: true},
		{name: "the mention is added to a step", head: "index+++", wantSilent: true},
		{name: "the mention is negated", head: "-", wantSilent: true},
		{name: "the mention is complemented", head: "~", wantSilent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, prefixed := prefixUse(tt.head, len(tt.head))
			if prefixed == tt.wantSilent {
				t.Fatalf("the text %q ahead of a mention says something = %t, want %t", tt.head, prefixed, !tt.wantSilent)
			}
			if got != tt.want {
				t.Errorf("the text %q ahead of a mention reaches the entry as %d, want %d", tt.head, got, tt.want)
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
