package main

import (
	"fmt"
	"strings"
)

// The helpers here read the statement and expression shapes the decompiler
// writes a logic surface method in. They recognize those shapes and nothing
// else, and report what they do not recognize: a shape read approximately
// decides a property the game leaves to live state, and the table has no way to
// say afterwards that the reading was a guess.

// switchGroup is one arm of a switch statement: the labels that reach it and
// the statements it runs. Consecutive labels with no statements between them
// share one group, which is how the game writes the wide property groups.
type switchGroup struct {
	labels []string
	body   string
}

// switchArm is one arm of a switch expression.
type switchArm struct {
	label  string
	result string
}

// isKeywordStatement reports whether a statement opens with the given keyword.
func isKeywordStatement(text, keyword string) bool {
	rest, ok := strings.CutPrefix(text, keyword)
	return ok && (rest == "" || !isIdentByte(rest[0]))
}

// splitIf separates an if statement into its condition and the block it guards.
// It ends at the block's closing brace, so an else the decompiler wrote is not
// part of what it returns; the evaluator meets that else as the next statement
// and rejects it there.
func splitIf(text string) (cond, body string, err error) {
	cond, end, err := matchDelim(text, 0, '(', ')')
	if err != nil {
		return "", "", fmt.Errorf("if condition: %w", err)
	}
	body, err = braceBlockAt(text[end:])
	if err != nil {
		return "", "", fmt.Errorf("if body: %w", err)
	}
	return cond, body, nil
}

// splitSwitch separates a switch statement into its subject and its body.
func splitSwitch(text string) (subject, body string, err error) {
	subject, end, err := matchDelim(text, 0, '(', ')')
	if err != nil {
		return "", "", fmt.Errorf("switch subject: %w", err)
	}
	body, err = braceBlockAt(text[end:])
	if err != nil {
		return "", "", fmt.Errorf("switch body: %w", err)
	}
	return strings.TrimSpace(subject), body, nil
}

// splitSwitchExpr recognizes the `subject switch { label => result, ... }`
// form and separates it into its arms. It reports false for an expression that
// is not a switch expression, which is not an error.
func splitSwitchExpr(expr string) (subject string, arms []switchArm, ok bool, err error) {
	head, rest, found := cutTopIdent(expr, "switch")
	if !found {
		return "", nil, false, nil
	}
	body, err := braceBlockAt(rest)
	if err != nil {
		return "", nil, false, fmt.Errorf("switch expression body: %w", err)
	}
	for _, entry := range splitExprList(body) {
		label, result, cut := cutTopOperator(entry, "=>")
		if !cut {
			return "", nil, false, fmt.Errorf("switch expression arm %.40q has no result: %w", entry, errUnsupportedForm)
		}
		arms = append(arms, switchArm{label: strings.TrimSpace(label), result: strings.TrimSpace(result)})
	}
	return strings.TrimSpace(head), arms, true, nil
}

// splitSwitchBody separates a switch statement's body into its arms, keeping
// the labels of an arm together with the statements they all reach.
func splitSwitchBody(body string) ([]switchGroup, error) {
	var (
		groups  []switchGroup
		pending []string
		start   = -1
	)
	// A run of labels with nothing between them reaches one arm, so a group is
	// only closed once statements have actually been seen.
	flush := func(end int) {
		if start < 0 || len(pending) == 0 || strings.TrimSpace(body[start:end]) == "" {
			return
		}
		groups = append(groups, switchGroup{labels: pending, body: body[start:end]})
		pending = nil
	}

	depth := 0
	for i := 0; i < len(body); i++ {
		if j := skipLiteral(body, i); j != i {
			i = j - 1
			continue
		}
		switch body[i] {
		case '(', '[', '{':
			depth++
			continue
		case ')', ']', '}':
			depth--
			continue
		}
		if depth != 0 || !isIdentStart(body, i) {
			continue
		}
		word, next := identAt(body, i)
		if word != "case" && word != "default" {
			i = next - 1
			continue
		}
		colon := indexTopByte(body[next:], ':')
		if colon < 0 {
			return nil, fmt.Errorf("switch label %q has no colon: %w", word, errNotFound)
		}
		flush(i)
		label := word
		if word == "case" {
			label = strings.TrimSpace(body[next : next+colon])
		}
		pending = append(pending, label)
		i = next + colon
		start = i + 1
	}
	flush(len(body))
	if len(groups) == 0 {
		return nil, fmt.Errorf("switch body has no arms: %w", errNotFound)
	}
	return groups, nil
}

// splitExprList splits a comma separated expression list.
//
// It differs from splitTop in not treating the angle brackets as a nesting
// level, which is what a list of switch expression arms needs: the arrow of an
// arm would otherwise read as a closing bracket and unbalance the rest.
func splitExprList(s string) []string {
	var parts []string
	depth, start := 0, 0
	emit := func(part string) {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	for i := 0; i < len(s); i++ {
		if j := skipLiteral(s, i); j != i {
			i = j - 1
			continue
		}
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				emit(s[start:i])
				start = i + 1
			}
		}
	}
	emit(s[start:])
	return parts
}

// indexTopByte returns the offset of the first b outside brackets and literals,
// or -1.
func indexTopByte(s string, b byte) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		if j := skipLiteral(s, i); j != i {
			i = j - 1
			continue
		}
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case b:
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// cutTopIdent splits s around the first occurrence of a bare identifier at
// bracket depth zero, returning the text before it and the text after.
func cutTopIdent(s, ident string) (before, after string, ok bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		if j := skipLiteral(s, i); j != i {
			i = j - 1
			continue
		}
		switch s[i] {
		case '(', '[', '{':
			depth++
			continue
		case ')', ']', '}':
			depth--
			continue
		}
		if depth != 0 || !isIdentStart(s, i) {
			continue
		}
		word, next := identAt(s, i)
		if word == ident {
			return s[:i], s[next:], true
		}
		i = next - 1
	}
	return "", "", false
}

// splitTopOperator splits an expression on every occurrence of a binary
// operator at bracket depth zero. A single element means the operator is absent.
func splitTopOperator(expr, op string) []string {
	var parts []string
	rest := expr
	for {
		lhs, tail, ok := cutTopOperator(rest, op)
		if !ok {
			return append(parts, rest)
		}
		parts = append(parts, lhs)
		rest = tail
	}
}

// cutTopOperator splits an expression around the first occurrence of a binary
// operator at bracket depth zero.
//
// An operator is only recognized where it is not part of a longer one, so that
// the equality in `a >= b` is not read as the `>` of a relation and the arrow of
// a switch arm is not read as an equality.
func cutTopOperator(expr, op string) (lhs, rhs string, ok bool) {
	depth := 0
	for i := 0; i < len(expr); i++ {
		if j := skipLiteral(expr, i); j != i {
			i = j - 1
			continue
		}
		switch expr[i] {
		case '(', '[', '{':
			depth++
			continue
		case ')', ']', '}':
			depth--
			continue
		}
		if depth != 0 || !strings.HasPrefix(expr[i:], op) {
			continue
		}
		if isOperatorByte(byteAt(expr, i-1)) || isOperatorByte(byteAt(expr, i+len(op))) {
			continue
		}
		return strings.TrimSpace(expr[:i]), strings.TrimSpace(expr[i+len(op):]), true
	}
	return "", "", false
}

// isOperatorByte reports whether b can be part of a C# operator token, which is
// what decides where one operator ends and the next begins.
func isOperatorByte(b byte) bool {
	return strings.IndexByte("=!<>&|+-*/%^?:", b) >= 0
}

// unwrapBlock removes the braces around a statement list that is nothing but
// one block, which is how the game writes a switch arm that declares a local.
func unwrapBlock(body string) string {
	for {
		trimmed := strings.TrimSpace(body)
		if !strings.HasPrefix(trimmed, "{") {
			return body
		}
		inner, end, err := matchDelim(trimmed, 0, '{', '}')
		if err != nil || strings.TrimSpace(trimmed[end:]) != "" {
			return body
		}
		body = inner
	}
}

// stripOuterParens removes the parentheses that wrap a whole expression.
func stripOuterParens(expr string) string {
	for strings.HasPrefix(expr, "(") {
		body, end, err := matchDelim(expr, 0, '(', ')')
		if err != nil || strings.TrimSpace(expr[end:]) != "" {
			return expr
		}
		expr = strings.TrimSpace(body)
	}
	return expr
}
