package sema

import (
	"context"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// AnalyzeToDepth is [Analyze] with the nesting bound given rather than shipped.
//
// The tests are in an external package, so this is the seam that lets them state
// a small bound and read what it refuses, and lets the stack probe state one no
// tree reaches so that what it measures is the recursion rather than the bound
// standing in front of it.
func AnalyzeToDepth(ctx context.Context, file *ast.File, tables Tables, maxDepth int) (*Program, source.DiagnosticList, error) {
	return analyze(ctx, file, tables, maxDepth)
}
