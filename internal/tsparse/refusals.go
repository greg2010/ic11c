package tsparse

import "github.com/greg2010/ic11c/internal/tsnode"

// refusals explains each construct the C grammar admits and MicroC does not.
// Every alternative the grammar declares for an expression, a statement, a
// type specifier and a declarator has either a converter or an entry here, so
// a construct C gains fails the build rather than reading as its neighbour.
var refusals = map[tsnode.Kind]string{
	tsnode.KindAlignasQualifier:          "alignas is not supported in MicroC; every value occupies one slot",
	tsnode.KindAlignofExpression:         "alignof is not supported in MicroC; every value occupies one slot",
	tsnode.KindAttributedDeclarator:      "an attribute on a declarator is not supported in MicroC",
	tsnode.KindAttributedStatement:       "an attribute on a statement is not supported in MicroC",
	tsnode.KindCommaExpression:           commaOperatorMsg,
	tsnode.KindCompoundLiteralExpression: "compound literals are not supported in MicroC",
	tsnode.KindConcatenatedString:        "adjacent string literals are not joined in MicroC; write one literal",
	tsnode.KindEnumSpecifier:             "enums are not supported in MicroC",
	tsnode.KindFieldExpression:           "member access is not supported in MicroC; structs and unions are excluded",
	tsnode.KindGenericExpression:         "_Generic is not supported in MicroC",
	tsnode.KindGotoStatement:             "goto is not supported in MicroC",
	tsnode.KindInitializerPair:           "a designated initializer is not supported in MicroC",
	tsnode.KindLabeledStatement:          "a label is not supported in MicroC, which has no goto to reach one",
	tsnode.KindLinkageSpecification:      "a linkage specification is not supported in MicroC",
	tsnode.KindMacroTypeSpecifier:        "a macro type specifier is not supported in MicroC",
	tsnode.KindNull:                      "nullptr is not supported in MicroC; a pointer names a slot and has no null",
	tsnode.KindParenthesizedDeclarator:   "a parenthesized declarator is not supported in MicroC; it spells a function pointer, which is excluded",
	tsnode.KindPreprocCall:               "preprocessor directives are not supported in MicroC",
	tsnode.KindPreprocDef:                "preprocessor directives are not supported in MicroC",
	tsnode.KindPreprocFunctionDef:        "preprocessor directives are not supported in MicroC",
	tsnode.KindPreprocIf:                 "preprocessor directives are not supported in MicroC",
	tsnode.KindPreprocIfdef:              "preprocessor directives are not supported in MicroC",
	tsnode.KindPreprocInclude:            "preprocessor directives are not supported in MicroC",
	tsnode.KindSizeofExpression:          "sizeof is not supported in MicroC",
	tsnode.KindStructSpecifier:           "structs are not supported in MicroC",
	tsnode.KindTypeDefinition:            "typedef is not supported in MicroC",
	tsnode.KindUnionSpecifier:            "unions are not supported in MicroC",
}

// The messages below are the ones [refusals] cannot hold: each is drawn from
// more than one place, or built into another message.

// commaOperatorMsg is drawn by the expression the grammar spells the operator
// with, and by the declarator list a declaration the grammar misread spells the
// same source as.
const commaOperatorMsg = "the comma operator is not supported in MicroC"

// nestedFuncMsg is drawn by a definition, which the grammar spells as its own
// construct, and by a declaration whose declarator turns out to name a
// parameter list.
const nestedFuncMsg = "nested function definitions are not supported in MicroC"

// blockPrototypeMsg is what a function declared but not defined inside a block
// draws. C admits the declaration and gives it block scope; MicroC has one
// scope for functions and writes every declaration of one in it.
const blockPrototypeMsg = "a function is declared at file scope in MicroC, not inside a block"

// statementExprMsg is what a block written where a value belongs draws. The
// statement expression is a compiler extension, and the C grammar admits one
// inside parentheses, an argument list and a parameter list alike.
const statementExprMsg = "a statement expression is not supported in MicroC"

// oldStyleParamsMsg is drawn by the parameter list, for the names it holds
// instead of parameters, and by the declarations behind it that would have
// given those names their types.
const oldStyleParamsMsg = "an old-style parameter list is not supported in MicroC; write each parameter's type inside the parentheses"

// multiDimArrayMsg is what an array of arrays draws. MicroC's memory is a flat
// row of slots, so a second bound has nothing to describe.
const multiDimArrayMsg = "multi-dimensional arrays are not supported in MicroC; index a flat array"

// oneSuffixMsg is what a declarator writing anything outside its array or its
// parameter list draws.
const oneSuffixMsg = "a declarator names one array or one parameter list in MicroC, and nothing outside it"

// attrSpelling is how a diagnostic shows what an attribute has to look like. It
// is built out of the spellings decl.go reads, so the sentence cannot come to
// offer one the front end would not accept.
const attrSpelling = "[[" + attrNamespace + "::" + prefabAttr + `("PrefabName")]]`

// unknownAttrMsg is drawn by the converter, for an attribute the grammar read
// whole, and by the reporting of an error node, for one it could not finish.
const unknownAttrMsg = "the only attribute MicroC recognizes is " + attrSpelling +
	", which states the prefab a dev declaration's pin is wired to"

// storageClassMsg is what a storage class draws. It has no entry in [refusals]
// because the sentence has to name the keyword: the keyword is the whole
// construct, C has seven of them, and a programmer told that storage classes
// are excluded is left to work out which word the compiler meant.
func storageClassMsg(spelling string) string {
	return "the '" + spelling + "' storage class is not supported in MicroC"
}

// unreachableWords are the constructs the grammar spells only with words this
// front end rewrites (see words.go), listed by every spelling each is written
// with rather than by node kind, since the grammar buries some of them inside
// a token whose node covers several words at once.
var unreachableWords = map[tsnode.Kind][]string{
	tsnode.KindAttributeSpecifier:  {"__attribute", "__attribute__"},
	tsnode.KindExtensionExpression: {"__extension__"},
	tsnode.KindGnuAsmExpression:    {"asm", "__asm", "__asm__"},
	tsnode.KindMsBasedModifier:     {"__based"},
	tsnode.KindMsCallModifier: {
		"__cdecl",
		"__clrcall",
		"__fastcall",
		"__stdcall",
		"__thiscall",
		"__vectorcall",
	},
	tsnode.KindMsDeclspecModifier: {"__declspec"},
	tsnode.KindMsPointerModifier:  {"_unaligned", "__unaligned", "__restrict", "__sptr", "__uptr"},
	tsnode.KindOffsetofExpression: {"offsetof"},
	tsnode.KindSehLeaveStatement:  {"__leave"},
	tsnode.KindSehTryStatement:    {"__try"},
}
