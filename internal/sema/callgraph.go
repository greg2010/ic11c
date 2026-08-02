package sema

// markRecursive sets [Func.Recursive] on every function that can reach itself
// through the call graph, directly or through a cycle. Later phases inline by
// default and need a real calling convention exactly for these, so the
// information is produced here, where the call edges are already in hand.
//
// It runs Tarjan's algorithm: a function is recursive when it calls itself or
// shares a strongly connected component with another function.
func markRecursive(funcs []*Func) {
	s := &sccFinder{
		index: make(map[*Func]int, len(funcs)),
		low:   make(map[*Func]int, len(funcs)),
		onStk: make(map[*Func]bool, len(funcs)),
	}
	for _, fn := range funcs {
		if _, seen := s.index[fn]; !seen {
			s.visit(fn)
		}
	}
}

type sccFinder struct {
	index map[*Func]int
	low   map[*Func]int
	onStk map[*Func]bool
	stack []*Func
	next  int
}

func (s *sccFinder) visit(fn *Func) {
	s.index[fn] = s.next
	s.low[fn] = s.next
	s.next++
	s.stack = append(s.stack, fn)
	s.onStk[fn] = true

	selfCall := false
	for _, callee := range fn.Callees {
		if callee == fn {
			selfCall = true
		}
		if _, seen := s.index[callee]; !seen {
			s.visit(callee)
			s.low[fn] = min(s.low[fn], s.low[callee])
			continue
		}
		if s.onStk[callee] {
			s.low[fn] = min(s.low[fn], s.index[callee])
		}
	}

	if s.low[fn] != s.index[fn] {
		return
	}
	top := len(s.stack) - 1
	for s.stack[top] != fn {
		top--
	}
	component := s.stack[top:]
	s.stack = s.stack[:top]
	recursive := len(component) > 1 || selfCall
	for _, member := range component {
		s.onStk[member] = false
		if recursive {
			member.Recursive = true
		}
	}
}
