package tsparse

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/lexer"
	"github.com/greg2010/ic11c/internal/tsnode"
)

// TestReservedWordsAreTheGrammarsAlone holds the derivation to both halves of
// what it claims: every word in it is one the grammar spells something with,
// and none is one MicroC reserves. A set that had drifted either way would
// rewrite a keyword the language has or leave a name the grammar eats.
func TestReservedWordsAreTheGrammarsAlone(t *testing.T) {
	if len(reservedWords) == 0 {
		t.Fatal("no words were derived, so no program the grammar mis-reads would be rewritten")
	}
	for word := range reservedWords {
		if !grammarWrites(word) {
			t.Errorf("%q is rewritten and the grammar spells nothing with it", word)
		}
		if tok := lexer.New("test.c", word).Next(); tok.Kind != lexer.Ident {
			t.Errorf("%q is rewritten and the lexer scans it as %v, so MicroC reserves it too", word, tok.Kind)
		}
	}
}

// grammarWrites reports whether a word appears in the grammar's own description
// of itself, as a kind it names a node for or as a literal one of its rules is
// written with.
func grammarWrites(word string) bool {
	if slices.Contains(tsnode.Spellings, word) {
		return true
	}
	return slices.Contains(tsnode.Tokens, tsnode.Kind(word))
}

// TestTheKindsReserveNoWordTheRulesOmit is what lets [reservedWords] read
// the grammar's rules alone: a word could in principle reach only the node
// kinds and not the rules, but none does, so reading both would answer the
// same set twice. A grammar that changes that fails here.
func TestTheKindsReserveNoWordTheRulesOmit(t *testing.T) {
	named := 0
	for _, kind := range tsnode.Tokens {
		word := string(kind)
		if !namesAVariable(word) {
			continue
		}
		named++
		if !slices.Contains(tsnode.Spellings, word) {
			t.Errorf("the grammar names a node for %q and no rule spells it, so the rules alone leave it unrewritten", word)
		}
	}
	if named == 0 {
		t.Error("no kind could name a variable, so this checked nothing about the derivation")
	}
}

// TestARewrittenWordBecomesAName checks the property the rewrite rests on: what
// stands in for a word is the same length, so no position moves, and is a name
// in its own right rather than another word the grammar reserves.
func TestARewrittenWordBecomesAName(t *testing.T) {
	c := newConverter("test.c", "void f(void) { long long asm = 1; }\n")
	if c.words == c.src {
		t.Fatal("the source holds a word the grammar reserves and nothing was rewritten")
	}
	if len(c.words) != len(c.src) {
		t.Fatalf("the rewritten source is %d bytes and the source is %d", len(c.words), len(c.src))
	}
	for word := range reservedWords {
		stand := strings.Repeat("_", len(word))
		if reservedWords[stand] {
			t.Errorf("%q stands in for %q and is itself a word the grammar reserves", stand, word)
		}
		if tok := lexer.New("test.c", stand).Next(); tok.Kind != lexer.Ident || tok.Text != stand {
			t.Errorf("%q stands in for %q and is not a name", stand, word)
		}
	}
}

// TestUnreachableWordsNameNoRefusal holds each row of [unreachableWords] to
// the claim it makes: a construct listed there is one no source can reach,
// so a refusal for it would be a sentence nothing draws, and a spelling the
// rewrite does not replace is a construct still reachable.
func TestUnreachableWordsNameNoRefusal(t *testing.T) {
	for kind, words := range unreachableWords {
		t.Run(string(kind), func(t *testing.T) {
			if len(words) == 0 {
				t.Fatalf("no word is listed for %s, so nothing says why it is unreachable", kind)
			}
			for _, word := range words {
				if !grammarWrites(word) {
					t.Errorf("%s is listed as spelled with %q and the grammar spells nothing with it", kind, word)
				}
				if !reservedWords[word] {
					t.Errorf("%s is spelled with %q, which the rewrite leaves alone", kind, word)
				}
			}
			if msg, refused := refusals[kind]; refused {
				t.Errorf("%s is unreachable and carries the refusal %q", kind, msg)
			}
		})
	}
}

// TestEveryRewrittenWordReadsAsAName is the end-to-end half: a program
// naming a variable after one of these words is read, and read as a name,
// in the two positions the grammar treats differently — an expression and a
// specifier.
func TestEveryRewrittenWordReadsAsAName(t *testing.T) {
	for _, word := range slices.Sorted(maps.Keys(reservedWords)) {
		t.Run(word, func(t *testing.T) {
			for _, src := range []string{
				"void f(void) { long long " + word + " = 1; " + word + " = 2; }\n",
				"long long f(long long a) { return a + " + word + "; }\n",
			} {
				_, diags, err := Parse("test.c", src)
				if err != nil {
					t.Fatalf("Parse failed: %v", err)
				}
				if len(diags) != 0 {
					t.Errorf("%q was refused:\n%s", src, diags)
				}
			}
		})
	}
}

// TestEveryWordTheGrammarSpellsCanNameAVariable asks the question the
// derivation cannot be asked about itself, walking the grammar's own
// vocabulary instead of the set the rewrite was built from: the grammar
// spells its boolean literals TRUE and FALSE as well as true and false.
func TestEveryWordTheGrammarSpellsCanNameAVariable(t *testing.T) {
	spelled := 0
	for _, word := range tsnode.Spellings {
		if !namesAVariable(word) {
			continue
		}
		spelled++
		t.Run(word, func(t *testing.T) {
			src := "long long f(void) { long long " + word + " = 7; return " + word + "; }\n"
			f, diags, err := Parse("test.c", src)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if len(diags) != 0 {
				t.Fatalf("%q was refused:\n%s", src, diags)
			}
			name, read := returnedName(t, f)
			if !read {
				return
			}
			if name.Name != word {
				t.Errorf("the return reads %q, want the variable %q", name.Name, word)
			}
		})
	}
	if spelled == 0 {
		t.Error("the grammar spells no word MicroC leaves alone, so this checked nothing")
	}
}

// returnedName gives the name the one function in f returns, and says whether it
// returned one. A return of anything else is reported here, since what the
// source wrote is a variable and nothing else can be right.
func returnedName(t *testing.T, f *ast.File) (*ast.Ident, bool) {
	t.Helper()
	if len(f.Decls) != 1 {
		t.Fatalf("got %d declarations, want 1", len(f.Decls))
	}
	fn, isFunc := f.Decls[0].(*ast.FuncDecl)
	if !isFunc || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("got %#v, want a function of two statements", f.Decls[0])
	}
	ret, returns := fn.Body.Stmts[1].(*ast.ReturnStmt)
	if !returns {
		t.Fatalf("got a %T, want a return", fn.Body.Stmts[1])
	}
	name, named := ret.Result.(*ast.Ident)
	if !named {
		t.Errorf("the return reads a %T, want the variable it names", ret.Result)
		return nil, false
	}
	return name, true
}
