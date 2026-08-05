package sema

import (
	"fmt"
	"testing"
)

// deepChain is how many functions the depth cases below build. It is far past
// anything a program writes and far past the 45 levels the shipped corpus
// nests, and it is the number the walk's depth is: the graphs are one path.
const deepChain = 100_000

// TestMarkRecursiveTakesADeepCallGraph holds the walk to a call graph whose
// longest path is the whole file.
//
// The depth of the walk is the number of functions declared, not the depth of
// any one declaration, and nothing bounds the first: a chain of mutually
// declared functions each calling the next is a program the front end reads and
// the nesting limit says nothing about. A walk that recursed would answer this
// with a stack the runtime cannot grow, which is fatal and unrecoverable rather
// than a diagnostic.
func TestMarkRecursiveTakesADeepCallGraph(t *testing.T) {
	tests := []struct {
		name string
		// link gives the callees of function i out of the whole chain.
		link func(funcs []*Func, i int) []*Func
		// want reports whether function i is expected to come back recursive.
		want func(i int) bool
	}{
		{
			name: "a chain that calls forward and never back",
			link: func(funcs []*Func, i int) []*Func {
				if i == len(funcs)-1 {
					return nil
				}
				return []*Func{funcs[i+1]}
			},
			want: func(int) bool { return false },
		},
		{
			name: "a chain whose last function closes the cycle",
			link: func(funcs []*Func, i int) []*Func {
				return []*Func{funcs[(i+1)%len(funcs)]}
			},
			want: func(int) bool { return true },
		},
		{
			name: "a chain whose last function calls itself",
			link: func(funcs []*Func, i int) []*Func {
				if i == len(funcs)-1 {
					return []*Func{funcs[i]}
				}
				return []*Func{funcs[i+1]}
			},
			want: func(i int) bool { return i == deepChain-1 },
		},
		{
			name: "a chain every function of which also calls the last",
			link: func(funcs []*Func, i int) []*Func {
				last := funcs[len(funcs)-1]
				if i == len(funcs)-1 {
					return nil
				}
				if i == len(funcs)-2 {
					return []*Func{last}
				}
				return []*Func{funcs[i+1], last}
			},
			want: func(int) bool { return false },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			funcs := chainOf(deepChain, tt.link)
			markRecursive(funcs)
			for i, fn := range funcs {
				if fn.Recursive != tt.want(i) {
					t.Fatalf("%s.Recursive = %v, want %v", fn.Name, fn.Recursive, tt.want(i))
				}
			}
		})
	}
}

// chainOf builds n functions and wires each one's callees with link.
func chainOf(n int, link func(funcs []*Func, i int) []*Func) []*Func {
	funcs := make([]*Func, n)
	for i := range funcs {
		funcs[i] = &Func{Name: fmt.Sprintf("f%d", i)}
	}
	for i := range funcs {
		funcs[i].Callees = link(funcs, i)
	}
	return funcs
}
