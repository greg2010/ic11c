package lexer

import "github.com/greg2010/ic11c/internal/source"

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

	// Reserved is a C23 keyword MicroC names no construct for. The keywords
	// above each get their own kind because a diagnostic has something
	// specific to say about each; these share one kind because the
	// diagnostic only needs to quote the spelling out of the token.
	Reserved

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
	Scope
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
	Reserved: "a reserved word",

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
	Scope:     "'::'",
	Question:  "'?'",
	Period:    "'.'",
	Arrow:     "'->'",
	Ellipsis:  "'...'",
}

// String renders k the way a diagnostic should name it: operator and keyword
// spellings come back quoted, everything else as a noun phrase.
func (k Kind) String() string { return source.EnumName(kindNames[:], int(k), "token") }

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

	// The rest of C23's keywords. MicroC spells nothing with them, but C
	// reserves every one, so a program declaring something named 'nullptr' is
	// not the valid C23 translation unit every MicroC program is.
	"_Alignas":       Reserved,
	"_Alignof":       Reserved,
	"_Atomic":        Reserved,
	"_BitInt":        Reserved,
	"_Bool":          Reserved,
	"_Complex":       Reserved,
	"_Decimal128":    Reserved,
	"_Decimal32":     Reserved,
	"_Decimal64":     Reserved,
	"_Generic":       Reserved,
	"_Imaginary":     Reserved,
	"_Noreturn":      Reserved,
	"_Static_assert": Reserved,
	"_Thread_local":  Reserved,
	"alignas":        Reserved,
	"alignof":        Reserved,
	"nullptr":        Reserved,
	"static_assert":  Reserved,
	"thread_local":   Reserved,
	"typeof":         Reserved,
	"typeof_unqual":  Reserved,
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
	// C23's attribute separator, and the only place two colons meet: no label
	// exists to be followed by one, and a conditional's colon is followed by an
	// expression.
	{"::", Scope},

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
// Int carries the decoded value of an IntLit or CharLit, Float of a
// FloatLit, and Str of a StringLit; each is zero for every other kind.
type Token struct {
	Kind  Kind
	Pos   source.Position
	Text  string
	Int   int64
	Float float64
	Str   string
}

// Describe names the token for a diagnostic, quoting the actual spelling of an
// identifier so the message points at something the programmer recognizes. A
// reserved word is quoted for the same reason: one kind stands for all of them,
// so only the text says which was written.
func (t Token) Describe() string {
	if t.Kind == Ident || t.Kind == Reserved {
		return "'" + t.Text + "'"
	}
	return t.Kind.String()
}
