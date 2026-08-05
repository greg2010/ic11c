package sema

// markRecursive sets [Func.Recursive] on every function that can reach
// itself through the call graph, directly or through a cycle — the ones
// later phases must give a real calling convention rather than inline. It
// runs Tarjan's algorithm.
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

// sccFrame is one function the walk is inside: how many of its callees it has
// stepped through, and whether one of them was itself.
type sccFrame struct {
	fn       *Func
	next     int
	selfCall bool
}

// visit walks everything reachable from root that has not been reached
// already, closing each strongly connected component as its root is left.
// The descent is a slice rather than a recursion: its depth is the number of
// functions the file declares, which ast.MaxNestingDepth does not bound.
func (s *sccFinder) visit(root *Func) {
	s.enter(root)
	work := []sccFrame{{fn: root}}
	for len(work) > 0 {
		top := &work[len(work)-1]
		fn := top.fn
		if top.next < len(fn.Callees) {
			callee := fn.Callees[top.next]
			top.next++
			switch {
			case callee == fn:
				top.selfCall = true
			case s.reached(callee):
				if s.onStk[callee] {
					s.low[fn] = min(s.low[fn], s.index[callee])
				}
			default:
				s.enter(callee)
				work = append(work, sccFrame{fn: callee})
			}
			continue
		}
		s.leave(fn, top.selfCall)
		work = work[:len(work)-1]
		if len(work) > 0 {
			caller := work[len(work)-1].fn
			s.low[caller] = min(s.low[caller], s.low[fn])
		}
	}
}

func (s *sccFinder) reached(fn *Func) bool {
	_, seen := s.index[fn]
	return seen
}

// enter numbers a function the walk has just reached and puts it on the
// component stack.
func (s *sccFinder) enter(fn *Func) {
	s.index[fn] = s.next
	s.low[fn] = s.next
	s.next++
	s.stack = append(s.stack, fn)
	s.onStk[fn] = true
}

// leave closes the component fn roots, if it roots one. selfCall says whether fn
// calls itself, which is what makes a component of one recursive.
func (s *sccFinder) leave(fn *Func, selfCall bool) {
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
