package main

import "strings"

// keptAttributes are the attributes a lifted declaration may carry into the
// compile unit; everything else is an engine or serialization marker (Unity
// inspector, game replication, Xml save format) that changes nothing a
// declaration computes. An allowlist, so a new marker attribute leaves the unit alone rather than breaking it.
var keptAttributes = map[string]bool{
	"Flags":             true,
	"Serializable":      true,
	"NonSerialized":     true,
	"Obsolete":          true,
	"StructLayout":      true,
	"CompilerGenerated": true,
	"MethodImpl":        true,
}

// stripAttrs removes the attribute lines a declaration carries that the unit
// does not keep, recording each removed name in dropped so the summary can
// report it.
func stripAttrs(dropped map[string]bool, text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		name, ok := attributeLine(line)
		if ok && !keptAttributes[name] {
			dropped[name] = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// attributeLine reports the attribute a line consists of, if it consists of
// one. A line carrying anything besides the attribute section is not one: an
// attribute written inline with the declaration it annotates cannot be removed
// without removing the declaration.
func attributeLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", false
	}
	name, _, ok := nextIdent(trimmed, 1)
	return name, ok
}
