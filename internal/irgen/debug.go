package irgen

import (
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// dwarfLangC99 is LLVMDWARFSourceLanguageC99. The binding's DwarfLang constants
// carry raw DWARF values, which the LLVMDWARFSourceLanguage enum the DIBuilder
// takes does not share, so the enum value is spelled numerically.
const dwarfLangC99 = llvm.DwarfLang(11)

// debugInfoVersion is the value LLVM requires under the "Debug Info Version"
// module flag. Debug metadata declared at any other version is discarded on
// the way through the module, taking every source position with it.
const debugInfoVersion = 3

// declareDebugInfo builds the compile unit, the file, and the subroutine type every subprogram
// shares. An inlined body keeps its own source lines but takes the scope of the definition it was
// spliced into, which is what lets a diagnostic point into a callee.
func (g *generator) declareDebugInfo(name, dir string) *llvm.DIBuilder {
	ctx := g.result.Context
	flag := ctx.MDNode([]llvm.Metadata{
		llvm.ConstInt(ctx.Int32Type(), 2, false).ConstantAsMetadata(),
		ctx.MDString("Debug Info Version"),
		llvm.ConstInt(ctx.Int32Type(), debugInfoVersion, false).ConstantAsMetadata(),
	})
	g.result.Module.AddNamedMetadataOperand("llvm.module.flags", flag)

	di := llvm.NewDIBuilder(g.result.Module)
	di.CreateCompileUnit(llvm.DICompileUnit{
		Language:  dwarfLangC99,
		File:      name,
		Dir:       dir,
		Producer:  "ic11c",
		Optimized: true,
	})
	g.file = di.CreateFile(name, dir)
	g.subroutine = di.CreateSubroutineType(llvm.DISubroutineType{
		File:       g.file,
		Parameters: []llvm.Metadata{di.CreateBasicType(scalarDebugType)},
	})
	return di
}

// scalarDebugType describes the type every MicroC value is stored as, following the lowering rather
// than the source spelling: a debugger reading DW_ATE_signed off a slot the machine fills with a
// double would render every value as the bit pattern of one.
var scalarDebugType = llvm.DIBasicType{Name: "double", SizeInBits: ic10.SlotBits, Encoding: llvm.DW_ATE_float}

func (g *generator) setLoc(pos source.Position) {
	if !pos.IsValid() {
		return
	}
	g.builder.SetCurrentDebugLocation(uint(pos.Line), uint(pos.Column), g.scope, g.inlinedAt)
	g.allocaBuilder.SetCurrentDebugLocation(uint(pos.Line), uint(pos.Column), g.scope, g.inlinedAt)
}

// enterInlineSite records that fn's body is being spliced in at pos, and gives the location every
// instruction of that body names as its call site. The scope stays the enclosing definition's
// subprogram rather than one per callee, since the callee's name is recorded in [Result.InlineSites]
// instead. The bindings expose no DILocation constructor, so one is read off an instruction created and erased just to attach it.
func (g *generator) enterInlineSite(pos source.Position, callee string) llvm.Metadata {
	if !pos.IsValid() {
		return g.inlinedAt
	}
	g.result.InlineSites[pos.LineCol()] = callee
	g.setLoc(pos)
	marker := g.builder.CreateAlloca(g.i64, "")
	site := marker.InstructionDebugLoc()
	marker.EraseFromParentAsInstruction()
	return site
}
