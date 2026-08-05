package ast

import "github.com/greg2010/ic11c/internal/source"

// MaxNestingDepth is how many nodes may enclose one another in a tree the
// compiler agrees to walk, refused before a deep enough chain exhausts the
// goroutine stack a recursive pass walks it with — a crash reports nothing.
// It comfortably exceeds real MicroC source (C23 5.2.4.1 requires only 63).
const MaxNestingDepth = 400_000

// DeeperThan reports where f nests one node inside another more than
// limit times, and whether it does; a file at exactly limit is read. The
// reported position is the outermost node the limit excludes, in reading
// order.
func DeeperThan(f *File, limit int) (source.Position, bool) {
	if f == nil {
		return source.Position{}, false
	}
	type frame struct {
		node  Node
		depth int
	}
	// The file is the root and its declarations are the first level, so limit
	// counts a declaration and everything written inside it. The seed is reversed
	// for the reason the push below is.
	var kids []Node
	for _, d := range f.Decls {
		kids = appendNode(kids, d)
	}
	stack := make([]frame, 0, len(kids))
	for i := len(kids) - 1; i >= 0; i-- {
		stack = append(stack, frame{node: kids[i], depth: 1})
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.depth > limit {
			return top.node.Pos(), true
		}
		kids = appendChildren(kids[:0], top.node)
		// Pushed in reverse so that the first child is the next one popped, which
		// is what makes the node reported the first past the limit in reading
		// order rather than the last.
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, frame{node: kids[i], depth: top.depth + 1})
		}
	}
	return source.Position{}, false
}

// appendChildren appends the nodes written directly inside n, in the
// order the source writes them. A missing case silently makes every
// tree read shallower than it is, letting a chain past [MaxNestingDepth]
// through.
func appendChildren(dst []Node, n Node) []Node {
	switch n := n.(type) {
	case *FuncDecl:
		dst = appendNode(dst, n.Result)
		for _, p := range n.Params {
			dst = appendNode(dst, p)
		}
		if n.Body != nil {
			dst = appendNode(dst, n.Body)
		}
	case *VarDecl:
		if n.Prefab != nil {
			dst = appendNode(dst, n.Prefab)
		}
		dst = appendNode(dst, n.Type)
		dst = appendNode(dst, n.Init)
	case *Param:
		dst = appendNode(dst, n.Type)

	case *BlockStmt:
		for _, s := range n.Stmts {
			dst = appendNode(dst, s)
		}
	case *ExprStmt:
		dst = appendNode(dst, n.X)
	case *IfStmt:
		dst = appendNode(dst, n.Cond)
		dst = appendNode(dst, n.Then)
		dst = appendNode(dst, n.Else)
	case *WhileStmt:
		dst = appendNode(dst, n.Cond)
		dst = appendNode(dst, n.Body)
	case *DoStmt:
		dst = appendNode(dst, n.Body)
		dst = appendNode(dst, n.Cond)
	case *ForStmt:
		dst = appendNode(dst, n.Init)
		dst = appendNode(dst, n.Cond)
		dst = appendNode(dst, n.Post)
		dst = appendNode(dst, n.Body)
	case *SwitchStmt:
		dst = appendNode(dst, n.Tag)
		for _, clause := range n.Cases {
			dst = appendNode(dst, clause)
		}
	case *CaseClause:
		dst = appendNode(dst, n.Value)
		for _, s := range n.Body {
			dst = appendNode(dst, s)
		}
	case *ReturnStmt:
		dst = appendNode(dst, n.Result)

	case *UnaryExpr:
		dst = appendNode(dst, n.X)
	case *IncDecExpr:
		dst = appendNode(dst, n.X)
	case *BinaryExpr:
		dst = appendNode(dst, n.X)
		dst = appendNode(dst, n.Y)
	case *AssignExpr:
		dst = appendNode(dst, n.Target)
		dst = appendNode(dst, n.Value)
	case *CondExpr:
		dst = appendNode(dst, n.Cond)
		dst = appendNode(dst, n.Then)
		dst = appendNode(dst, n.Else)
	case *IndexExpr:
		dst = appendNode(dst, n.X)
		dst = appendNode(dst, n.Index)
	case *CallExpr:
		dst = appendNode(dst, n.Fun)
		for _, a := range n.Args {
			dst = appendNode(dst, a)
		}
	case *CastExpr:
		dst = appendNode(dst, n.Type)
		dst = appendNode(dst, n.X)
	case *InitListExpr:
		for _, e := range n.Elems {
			dst = appendNode(dst, e)
		}

	case *PointerType:
		dst = appendNode(dst, n.Elem)
	case *ArrayType:
		dst = appendNode(dst, n.Elem)
		dst = appendNode(dst, n.Size)
	}
	return dst
}

// appendNode appends one child, skipping the field a node left unwritten: an
// else clause, a bare return's result, a declaration's initializer, an unsized
// array's bound.
func appendNode(dst []Node, n Node) []Node {
	if n == nil {
		return dst
	}
	return append(dst, n)
}
