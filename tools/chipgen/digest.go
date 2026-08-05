package main

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// checkedDigest is the fingerprint of the decompiled source under gamesrc/
// this slicer was last read against. It is embedded rather than read off disk
// so that deleting it is a compile error, since a gate removable by removing a
// file stops running without anything downstream noticing.

//go:embed slice.digest
var checkedDigest string

// digestFile is where the update writes, relative to the module root.
const digestFile = "tools/chipgen/slice.digest"

// digestBytes is how much of the SHA-256 a record keeps. This is a change
// detector a human reads in a diff, not a security boundary, so eight bytes is
// enough to make an accidental collision across a few thousand records
// implausible while keeping a line short.
const digestBytes = 8

// Record kinds. A shape says what a type declares; a cut says what one
// construct the slicer consumes is made of.
const (
	shapeKind = "shape"
	cutKind   = "cut"
)

// record is one line of the digest.
type record struct {
	kind   string
	path   string
	digest string
}

// ledger collects a fingerprint for every construct one slice consumed. The
// slicer refuses when a construct is not findable; the ledger adds the
// other half — a construct still findable whose text is not what it was.
// Without it a game update rewriting a copied body would leave the slice succeeding silently.
type ledger struct {
	records map[string]record
}

func newLedger() *ledger {
	return &ledger{records: make(map[string]record)}
}

// shape records what a type declares: its keyword, base list and member
// signatures, without bodies. This is what holds the slicer's claims about
// a type it does not lift whole — that a collapsed chain's middle class
// declares nothing the collapse assumes, and that a keep list still covers a type that has grown.
func (l *ledger) shape(path string, d decl, members []decl) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s : %s\n", d.keyword, d.name, strings.Join(d.bases, ", "))
	signatures := make([]string, len(members))
	for i, m := range members {
		signatures[i] = m.name
	}
	slices.Sort(signatures)
	for _, signature := range signatures {
		b.WriteString(signature)
		b.WriteByte('\n')
	}
	return l.add(shapeKind, path, textDigest(b.String()))
}

// cut records the text of one construct the slice consumes, whether
// copied, rewritten, or read and left behind. Where a construct is dropped
// or replaced, what stands in its place is a claim about what was there —
// exactly what a game update can falsify without moving a signature.
func (l *ledger) cut(path, text string) error {
	return l.add(cutKind, path, declDigest(text))
}

// cutTree records a declaration and, when it is a type, everything nested
// inside it, so that a change names the member it happened in rather than the
// class around it.
func (l *ledger) cutTree(path string, d decl) error {
	if err := l.cut(path, d.text); err != nil {
		return err
	}
	if d.kind != declContainer {
		return nil
	}
	nested, err := splitDecls(d.body)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, n := range nested {
		if err := l.cutTree(path+"/"+n.name, n); err != nil {
			return err
		}
	}
	return nil
}

// add refuses two different fingerprints under one path. The slicer reads
// several constructs more than once, and the same path answering differently
// would mean a record whose meaning depends on the order the slice ran in.
func (l *ledger) add(kind, path, digest string) error {
	key := kind + " " + path
	if was, seen := l.records[key]; seen && was.digest != digest {
		return fmt.Errorf("%s %s fingerprints as both %s and %s within one slice", kind, path, was.digest, digest)
	}
	l.records[key] = record{kind: kind, path: path, digest: digest}
	return nil
}

// sorted returns the records in the order the digest writes them.
func (l *ledger) sorted() []record {
	out := slices.Collect(maps.Values(l.records))
	slices.SortFunc(out, func(a, b record) int {
		if c := strings.Compare(a.path, b.path); c != 0 {
			return c
		}
		return strings.Compare(a.kind, b.kind)
	})
	return out
}

// declDigest fingerprints a declaration. Comments are removed and layout
// whitespace collapsed first, so re-indenting or renesting a construct is
// not a change; comments go because the provenance comment every lifted
// slice carries names its own line range, which a comment shift would otherwise disturb.
func declDigest(text string) string {
	return textDigest(stripComments(text))
}

func textDigest(text string) string {
	var normalized strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		normalized.WriteString(line)
		normalized.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(normalized.String()))
	return hex.EncodeToString(sum[:digestBytes])
}

// stripComments removes C# comments, leaving string and character literals
// alone: the patterns the preprocessor compiles are string literals, and a
// slash inside one is not the start of a comment.
func stripComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		j := skipLiteral(src, i)
		if j == i {
			b.WriteByte(src[i])
			i++
			continue
		}
		if src[i] == '/' {
			b.WriteByte('\n')
		} else {
			b.WriteString(src[i:j])
		}
		i = j
	}
	return b.String()
}

// digestHeader introduces the file for whoever opens it without having read the
// procedure first.
const digestHeader = `# Fingerprints of the decompiled game source under gamesrc/ that tools/chipgen
# cuts the chip from. One record per construct: a shape is what a type declares,
# a cut is text the slicer copies, rewrites, or reads and leaves behind. The
# digest is the leading eight bytes of a SHA-256 over that text with comments
# and layout whitespace removed.
`

// renderDigest writes the ledger in the form the file is checked in as.
//
// The count is written alongside the records so that a truncated file is
// refused rather than read as a shorter list of things to check.
func renderDigest(l *ledger, dropped map[string]bool) string {
	var b strings.Builder
	b.WriteString(digestHeader)
	fmt.Fprintf(&b, "attributes %s\n", strings.Join(sortedKeys(dropped), " "))
	records := l.sorted()
	fmt.Fprintf(&b, "records %d\n\n", len(records))
	for _, r := range records {
		fmt.Fprintf(&b, "%s %s %s\n", r.kind, r.digest, r.path)
	}
	return b.String()
}

// checkpoint is a parsed digest.
type checkpoint struct {
	attributes string
	records    map[string]record
}

func parseDigest(text string) (*checkpoint, error) {
	parsed := &checkpoint{records: make(map[string]record)}
	count := -1
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		field := strings.TrimRight(scanner.Text(), " \t")
		if field == "" || strings.HasPrefix(field, "#") {
			continue
		}
		switch head, rest, _ := strings.Cut(field, " "); head {
		case "attributes":
			parsed.attributes = rest
		case "records":
			n, err := strconv.Atoi(rest)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: record count %q is not a number", digestFile, line, rest)
			}
			count = n
		case shapeKind, cutKind:
			digest, path, ok := strings.Cut(rest, " ")
			if !ok || path == "" {
				return nil, fmt.Errorf("%s:%d: %s record names no path", digestFile, line, head)
			}
			parsed.records[head+" "+path] = record{kind: head, path: path, digest: digest}
		default:
			return nil, fmt.Errorf("%s:%d: unknown record kind %q", digestFile, line, head)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", digestFile, err)
	}
	if count < 0 {
		return nil, fmt.Errorf("%s: no record count, so a truncated file would read as a shorter one", digestFile)
	}
	if count != len(parsed.records) {
		return nil, fmt.Errorf("%s: says it holds %d records and holds %d", digestFile, count, len(parsed.records))
	}
	if count == 0 {
		return nil, fmt.Errorf("%s: holds no records, so it would be satisfied by a slice that cut nothing", digestFile)
	}
	return parsed, nil
}

// reported is how many changed paths a refusal names before it stops counting.
// Long enough that an ordinary game update is enumerated in full, short enough
// that a decompiler change does not bury the summary.
const reported = 40

// compareDigest refuses a decompile the checked-in digest does not
// describe. The three lists mean different things: a changed record is a
// construct still findable and no longer what it was, the one case this
// exists for; an added or removed record is a change to this tree's own slice rather than to the game.
func compareDigest(l *ledger, dropped map[string]bool, want *checkpoint) error {
	var changed, added, removed []string
	for key, got := range l.records {
		switch was, known := want.records[key]; {
		case !known:
			added = append(added, key)
		case was.digest != got.digest:
			changed = append(changed, key)
		}
	}
	for key := range want.records {
		if _, still := l.records[key]; !still {
			removed = append(removed, key)
		}
	}

	var b strings.Builder
	for _, section := range []struct {
		what  string
		paths []string
	}{
		{"changed since the digest was taken", changed},
		{"cut now and not named by the digest", added},
		{"named by the digest and not cut now", removed},
	} {
		if len(section.paths) == 0 {
			continue
		}
		slices.Sort(section.paths)
		fmt.Fprintf(&b, "\n  %d %s:", len(section.paths), section.what)
		for i, path := range section.paths {
			if i == reported {
				fmt.Fprintf(&b, "\n\t... and %d more", len(section.paths)-reported)
				break
			}
			fmt.Fprintf(&b, "\n\t%s", path)
		}
	}
	if got := strings.Join(sortedKeys(dropped), " "); got != want.attributes {
		fmt.Fprintf(&b, "\n  attributes dropped from the lifted declarations are now %q, the digest names %q", got, want.attributes)
	}
	if b.Len() == 0 {
		return nil
	}
	return fmt.Errorf("the decompiled source is not the one %s was taken from:%s\n\n"+
		"Read the C# behind each name before regenerating; a body that is still found and no "+
		"longer says what it said is a chip that answers differently with nothing else in the "+
		"tree noticing. Regenerate with --update-digest once each one has been read", digestFile, b.String())
}
