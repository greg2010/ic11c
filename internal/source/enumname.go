package source

import "strconv"

// EnumName renders v out of names, answering with typeName and the number
// when v has no entry — either past the end, or an unnamed gap left by an
// iota block skipping a value. The number keeps a gap visible: an empty
// string would let an unnamed constant read as nothing in a diagnostic.
func EnumName(names []string, v int, typeName string) string {
	if v >= 0 && v < len(names) && names[v] != "" {
		return names[v]
	}
	return typeName + "(" + strconv.Itoa(v) + ")"
}
