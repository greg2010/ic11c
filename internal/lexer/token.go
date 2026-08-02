package lexer

import (
	"strconv"

	"github.com/greg2010/ic11c/internal/source"
)

// Kind classifies a token.
type Kind uint8

// Token kinds. Keywords MicroC excludes are still scanned as keywords rather
// than identifiers, so the parser can reject them by name.
const (
	// EOF terminates every token stream. Next returns it indefinitely once the
	// source is exhausted.
	EOF Kind = iota

	Ident
	IntLit
	FloatLit
	CharLit
	StringLit

	Bool
	Break
	Case
	Const
	Constexpr
	Continue
	Default
	Dev
	Do
	Double
	Else
	False
	For
	If
	Int
	Return
	Switch
	True
	Void
	While

	Auto
	Char
	Enum
	Extern
	Float
	Goto
	Inline
	Long
	Register
	Restrict
	Short
	Signed
	Sizeof
	Static
	Struct
	Typedef
	Union
	Unsigned
	Volatile

	Add
	Sub
	Mul
	Quo
	Rem
	And
	Or
	Xor
	Tilde
	Not
	Shl
	Shr
	Land
	Lor
	Inc
	Dec

	Assign
	AddAssign
	SubAssign
	MulAssign
	QuoAssign
	RemAssign
	AndAssign
	OrAssign
	XorAssign
	ShlAssign
	ShrAssign

	Eq
	Neq
	Lt
	Leq
	Gt
	Geq

	Lparen
	Rparen
	Lbrack
	Rbrack
	Lbrace
	Rbrace
	Comma
	Semicolon
	Colon
	Question
	Period
	Arrow
	Ellipsis
)

// kindNames renders kinds for diagnostics. Operator and keyword spellings are
// pre-quoted so that "expected ';'" and "expected an identifier" both read
// correctly from the same table.
var kindNames = [...]string{
	EOF:       "end of file",
	Ident:     "an identifier",
	IntLit:    "an integer literal",
	FloatLit:  "a floating-point literal",
	CharLit:   "a character literal",
	StringLit: "a string literal",

	Bool:      "'bool'",
	Break:     "'break'",
	Case:      "'case'",
	Const:     "'const'",
	Constexpr: "'constexpr'",
	Continue:  "'continue'",
	Default:   "'default'",
	Dev:       "'dev'",
	Do:        "'do'",
	Double:    "'double'",
	Else:      "'else'",
	False:     "'false'",
	For:       "'for'",
	If:        "'if'",
	Int:       "'int'",
	Return:    "'return'",
	Switch:    "'switch'",
	True:      "'true'",
	Void:      "'void'",
	While:     "'while'",

	Auto:     "'auto'",
	Char:     "'char'",
	Enum:     "'enum'",
	Extern:   "'extern'",
	Float:    "'float'",
	Goto:     "'goto'",
	Inline:   "'inline'",
	Long:     "'long'",
	Register: "'register'",
	Restrict: "'restrict'",
	Short:    "'short'",
	Signed:   "'signed'",
	Sizeof:   "'sizeof'",
	Static:   "'static'",
	Struct:   "'struct'",
	Typedef:  "'typedef'",
	Union:    "'union'",
	Unsigned: "'unsigned'",
	Volatile: "'volatile'",

	Add:   "'+'",
	Sub:   "'-'",
	Mul:   "'*'",
	Quo:   "'/'",
	Rem:   "'%'",
	And:   "'&'",
	Or:    "'|'",
	Xor:   "'^'",
	Tilde: "'~'",
	Not:   "'!'",
	Shl:   "'<<'",
	Shr:   "'>>'",
	Land:  "'&&'",
	Lor:   "'||'",
	Inc:   "'++'",
	Dec:   "'--'",

	Assign:    "'='",
	AddAssign: "'+='",
	SubAssign: "'-='",
	MulAssign: "'*='",
	QuoAssign: "'/='",
	RemAssign: "'%='",
	AndAssign: "'&='",
	OrAssign:  "'|='",
	XorAssign: "'^='",
	ShlAssign: "'<<='",
	ShrAssign: "'>>='",

	Eq:  "'=='",
	Neq: "'!='",
	Lt:  "'<'",
	Leq: "'<='",
	Gt:  "'>'",
	Geq: "'>='",

	Lparen:    "'('",
	Rparen:    "')'",
	Lbrack:    "'['",
	Rbrack:    "']'",
	Lbrace:    "'{'",
	Rbrace:    "'}'",
	Comma:     "','",
	Semicolon: "';'",
	Colon:     "':'",
	Question:  "'?'",
	Period:    "'.'",
	Arrow:     "'->'",
	Ellipsis:  "'...'",
}

// String renders k the way a diagnostic should name it: operator and keyword
// spellings come back quoted, everything else as a noun phrase.
func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "token(" + strconv.Itoa(int(k)) + ")"
}

var keywords = map[string]Kind{
	"bool":      Bool,
	"break":     Break,
	"case":      Case,
	"const":     Const,
	"constexpr": Constexpr,
	"continue":  Continue,
	"default":   Default,
	"dev":       Dev,
	"do":        Do,
	"double":    Double,
	"else":      Else,
	"false":     False,
	"for":       For,
	"if":        If,
	"int":       Int,
	"return":    Return,
	"switch":    Switch,
	"true":      True,
	"void":      Void,
	"while":     While,

	"auto":     Auto,
	"char":     Char,
	"enum":     Enum,
	"extern":   Extern,
	"float":    Float,
	"goto":     Goto,
	"inline":   Inline,
	"long":     Long,
	"register": Register,
	"restrict": Restrict,
	"short":    Short,
	"signed":   Signed,
	"sizeof":   Sizeof,
	"static":   Static,
	"struct":   Struct,
	"typedef":  Typedef,
	"union":    Union,
	"unsigned": Unsigned,
	"volatile": Volatile,
}

// operators is ordered longest spelling first so that a linear prefix scan is a
// maximal munch.
var operators = []struct {
	text string
	kind Kind
}{
	{"<<=", ShlAssign},
	{">>=", ShrAssign},
	{"...", Ellipsis},

	{"==", Eq},
	{"!=", Neq},
	{"<=", Leq},
	{">=", Geq},
	{"&&", Land},
	{"||", Lor},
	{"<<", Shl},
	{">>", Shr},
	{"++", Inc},
	{"--", Dec},
	{"->", Arrow},
	{"+=", AddAssign},
	{"-=", SubAssign},
	{"*=", MulAssign},
	{"/=", QuoAssign},
	{"%=", RemAssign},
	{"&=", AndAssign},
	{"|=", OrAssign},
	{"^=", XorAssign},

	{"+", Add},
	{"-", Sub},
	{"*", Mul},
	{"/", Quo},
	{"%", Rem},
	{"&", And},
	{"|", Or},
	{"^", Xor},
	{"~", Tilde},
	{"!", Not},
	{"<", Lt},
	{">", Gt},
	{"=", Assign},
	{"(", Lparen},
	{")", Rparen},
	{"[", Lbrack},
	{"]", Rbrack},
	{"{", Lbrace},
	{"}", Rbrace},
	{",", Comma},
	{";", Semicolon},
	{":", Colon},
	{"?", Question},
	{".", Period},
}

// Token is one lexical element together with where it was written.
//
// Int carries the decoded value of an IntLit or CharLit, Float the decoded
// value of a FloatLit, and Str the decoded bytes of a StringLit; each is zero
// for every other kind. Text is always the raw source spelling, so a diagnostic
// can quote what the programmer wrote.
type Token struct {
	Kind  Kind
	Pos   source.Position
	Text  string
	Int   int64
	Float float64
	Str   string
}

// Describe names the token for a diagnostic, quoting the actual spelling of an
// identifier so the message points at something the programmer recognizes.
func (t Token) Describe() string {
	if t.Kind == Ident {
		return "'" + t.Text + "'"
	}
	return t.Kind.String()
}
