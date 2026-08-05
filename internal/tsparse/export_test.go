package tsparse

import ts "github.com/tree-sitter/go-tree-sitter"

// ReadsAsC reports whether the C grammar reads src whole, with no error node
// anywhere in the tree — what tells a refusal about the language apart from
// one about a string that was never C. It is exported for the tests in
// tsparse_test, which cannot reach the compiled grammar any other way.
func ReadsAsC(src string) bool {
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(language); err != nil {
		return false
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		return false
	}
	defer tree.Close()
	return !tree.RootNode().HasError()
}
