package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Offsets within the resource fixture. The three directory levels and the data
// entry are laid out back to back, each holding one entry.
const (
	fixtureTypeDir  = 0
	fixtureNameDir  = 24
	fixtureLangDir  = 48
	fixtureDataDir  = 72
	fixtureInfoAt   = 88
	fixtureInfoSize = 64
)

// fixtureVersion is the version the default fixture encodes.
const fixtureVersion = "0.2.6403.27689"

// resourceOption mutates a fixture after it is laid out, so a test can state
// the one field it is perturbing.
type resourceOption func([]byte)

// resourceFixture builds the bytes of a .rsrc section carrying one RT_VERSION
// resource, rebased on the given virtual address the way a real section is.
func resourceFixture(virtualAddress uint32, opts ...resourceOption) []byte {
	rsrc := make([]byte, fixtureInfoAt+fixtureInfoSize)
	putDirectory(rsrc, fixtureTypeDir, 0, 1)
	putEntry(rsrc, fixtureTypeDir+resourceDirSize, rtVersion, fixtureNameDir|resourceSubdirFlag)
	putDirectory(rsrc, fixtureNameDir, 0, 1)
	putEntry(rsrc, fixtureNameDir+resourceDirSize, 1, fixtureLangDir|resourceSubdirFlag)
	putDirectory(rsrc, fixtureLangDir, 0, 1)
	putEntry(rsrc, fixtureLangDir+resourceDirSize, 1033, fixtureDataDir)

	binary.LittleEndian.PutUint32(rsrc[fixtureDataDir:], virtualAddress+fixtureInfoAt)
	binary.LittleEndian.PutUint32(rsrc[fixtureDataDir+4:], fixtureInfoSize)

	// The signature sits past the start of the blob so the scan for it is
	// exercised rather than hit on the first word.
	const at = fixtureInfoAt + 8
	binary.LittleEndian.PutUint32(rsrc[at:], fixedFileInfoMagic)
	binary.LittleEndian.PutUint32(rsrc[at+8:], 0<<16|2)
	binary.LittleEndian.PutUint32(rsrc[at+12:], 6403<<16|27689)

	for _, opt := range opts {
		opt(rsrc)
	}
	return rsrc
}

// putVersion restamps a resource fixture with another build's four numbers, so
// a test that lays two resources in one image can tell which one was read.
func putVersion(major, minor, build, revision uint16) resourceOption {
	return func(rsrc []byte) {
		const at = fixtureInfoAt + 8
		binary.LittleEndian.PutUint32(rsrc[at+8:], uint32(major)<<16|uint32(minor))
		binary.LittleEndian.PutUint32(rsrc[at+12:], uint32(build)<<16|uint32(revision))
	}
}

func putDirectory(rsrc []byte, at int, named, ids uint16) {
	binary.LittleEndian.PutUint16(rsrc[at+12:], named)
	binary.LittleEndian.PutUint16(rsrc[at+14:], ids)
}

func putEntry(rsrc []byte, at int, id, offset uint32) {
	binary.LittleEndian.PutUint32(rsrc[at:], id)
	binary.LittleEndian.PutUint32(rsrc[at+4:], offset)
}

func TestFindVersionInfo(t *testing.T) {
	const virtualAddress = 0x2000

	tests := []struct {
		name    string
		opts    []resourceOption
		wantErr string
	}{
		{name: "well formed resource"},
		{
			name: "no RT_VERSION entry",
			opts: []resourceOption{func(r []byte) {
				putEntry(r, fixtureTypeDir+resourceDirSize, rtVersion+1, fixtureNameDir|resourceSubdirFlag)
			}},
			wantErr: "locate RT_VERSION",
		},
		{
			name:    "empty name directory",
			opts:    []resourceOption{func(r []byte) { putDirectory(r, fixtureNameDir, 0, 0) }},
			wantErr: "locate version resource name",
		},
		{
			name:    "language level is another directory",
			opts:    []resourceOption{func(r []byte) { putEntry(r, fixtureLangDir+resourceDirSize, 1033, fixtureDataDir|resourceSubdirFlag) }},
			wantErr: "language level is a subdirectory",
		},
		{
			name:    "data RVA precedes the section",
			opts:    []resourceOption{func(r []byte) { binary.LittleEndian.PutUint32(r[fixtureDataDir:], virtualAddress-1) }},
			wantErr: "precedes .rsrc",
		},
		{
			name:    "data runs past the section",
			opts:    []resourceOption{func(r []byte) { binary.LittleEndian.PutUint32(r[fixtureDataDir+4:], fixtureInfoSize+1) }},
			wantErr: "extends past .rsrc",
		},
		{
			name:    "directory claims more entries than fit",
			opts:    []resourceOption{func(r []byte) { putDirectory(r, fixtureTypeDir, 0, 4096) }},
			wantErr: "past end of .rsrc",
		},
		{
			// A bound that overflows a signed thirty-two bit word. The reading this
			// is joined against widens to sixty-four bits whatever word it runs on,
			// so a narrower bound here panics on one build and refuses on another.
			name:    "data entry offset overflows a thirty-two bit bound",
			opts:    []resourceOption{func(r []byte) { putEntry(r, fixtureLangDir+resourceDirSize, 1033, 0x7FFFFFF8) }},
			wantErr: "data entry out of range",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := findVersionInfo(resourceFixture(virtualAddress, tt.opts...), virtualAddress)
			if !checkErr(t, "findVersionInfo", err, tt.wantErr) {
				return
			}
			got, err := fixedFileVersion(info)
			if err != nil {
				t.Fatalf("fixedFileVersion: %v", err)
			}
			if got != fixtureVersion {
				t.Errorf("fixedFileVersion = %q, want %q", got, fixtureVersion)
			}
		})
	}
}

func TestFixedFileVersionErrors(t *testing.T) {
	tests := []struct {
		name    string
		info    []byte
		wantErr string
	}{
		{name: "no signature", info: make([]byte, fixtureInfoSize), wantErr: "VS_FIXEDFILEINFO signature"},
		{
			name: "signature too near the end",
			info: func() []byte {
				info := make([]byte, fixedFileInfoMinSize)
				binary.LittleEndian.PutUint32(info[4:], fixedFileInfoMagic)
				return info
			}(),
			wantErr: "VS_FIXEDFILEINFO truncated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixedFileVersion(tt.info)
			checkErr(t, "fixedFileVersion", err, tt.wantErr)
		})
	}
}

func TestReadAssemblyVersionRejectsANonImage(t *testing.T) {
	path := filepath.Join("testdata", "full.json")
	_, err := readAssemblyVersion(path)
	checkErr(t, "readAssemblyVersion", err, "open PE image")
}

// What a fixture image declares about itself. The machine is one debug/pe
// recognises, and the section table's place is a function of the four numbers.
const (
	fixtureMachine     = 0x014c
	fixtureOptMagic    = 0x10b
	fixtureDirCountAt  = 92
	fixtureDirectories = 16
	fixtureSectionSize = 0x200
	fixtureTextVA      = 0x2000
	fixtureRsrcVA      = 0x4000
)

// imageLayout is where a fixture image puts the structures a reader walks. Take
// one from newImageLayout; the zero value describes nothing.
type imageLayout struct {
	// headerAt is where the COFF file header sits. Zero puts it at the start with
	// no DOS signature, which debug/pe reads as a bare object file.
	headerAt    int
	sections    int
	optSize     int
	directories uint32
}

func newImageLayout() imageLayout {
	return imageLayout{
		headerAt:    0x84,
		sections:    2,
		optSize:     optionalHeader32Size,
		directories: fixtureDirectories,
	}
}

func (l imageLayout) tableAt() int { return l.headerAt + fileHeaderSize + l.optSize }

// dataAt is where the sections' bytes begin, which is past the last section
// header however many the image declares.
func (l imageLayout) dataAt() int {
	end := l.tableAt() + l.sections*sectionHeaderSize
	return (end + fixtureSectionSize - 1) &^ (fixtureSectionSize - 1)
}

func (l imageLayout) textAt() int { return l.dataAt() }
func (l imageLayout) rsrcAt() int { return l.dataAt() + fixtureSectionSize }

// imageOption mutates a fixture image after it is laid out, so a test can state
// the one field it is perturbing.
type imageOption func(imageLayout, []byte)

// build lays out a PE image carrying a .text section and a .rsrc section, the
// second holding one well formed RT_VERSION resource.
func (l imageLayout) build(opts ...imageOption) []byte {
	rsrc := resourceFixture(fixtureRsrcVA)
	image := make([]byte, l.rsrcAt()+fixtureSectionSize)

	if l.headerAt > 0 {
		copy(image, dosSignature)
		binary.LittleEndian.PutUint32(image[lfanewAt:], uint32(l.headerAt-peSignatureSize))
		copy(image[l.headerAt-peSignatureSize:], "PE\x00\x00")
	}
	binary.LittleEndian.PutUint16(image[l.headerAt:], fixtureMachine)
	binary.LittleEndian.PutUint16(image[l.headerAt+2:], uint16(l.sections))
	binary.LittleEndian.PutUint16(image[l.headerAt+16:], uint16(l.optSize))

	optional := l.headerAt + fileHeaderSize
	binary.LittleEndian.PutUint16(image[optional:], fixtureOptMagic)
	binary.LittleEndian.PutUint32(image[optional+fixtureDirCountAt:], l.directories)

	putSection(image, l.tableAt(), ".text", fixtureTextVA, fixtureSectionSize, uint32(l.textAt()))
	putSection(image, l.tableAt()+sectionHeaderSize, ".rsrc", fixtureRsrcVA, uint32(len(rsrc)), uint32(l.rsrcAt()))
	copy(image[l.rsrcAt():], rsrc)

	for _, apply := range opts {
		apply(l, image)
	}
	return image
}

func putSection(image []byte, at int, name string, virtualAddress, size, offset uint32) {
	copy(image[at:at+sectionNameSize], name)
	binary.LittleEndian.PutUint32(image[at+12:], virtualAddress)
	binary.LittleEndian.PutUint32(image[at+16:], size)
	binary.LittleEndian.PutUint32(image[at+20:], offset)
}

// renameSection respells a section's eight name bytes, padding included, so a
// test can state a spelling no linker emits.
func renameSection(i int, name []byte) imageOption {
	return func(l imageLayout, image []byte) {
		at := l.tableAt() + i*sectionHeaderSize
		clear(image[at : at+sectionNameSize])
		copy(image[at:at+sectionNameSize], name)
	}
}

// shadowResource lays a second, well formed version resource over the .text
// section, stamped so a reading that picks it answers a unique number.
func shadowResource(major, minor, build, revision uint16) imageOption {
	return func(l imageLayout, image []byte) {
		copy(image[l.textAt():], resourceFixture(fixtureTextVA, putVersion(major, minor, build, revision)))
	}
}

// restamp rewrites the four numbers the image's real resource carries.
func restamp(major, minor, build, revision uint16) imageOption {
	return func(l imageLayout, image []byte) {
		copy(image[l.rsrcAt():], resourceFixture(fixtureRsrcVA, putVersion(major, minor, build, revision)))
	}
}

func writeImage(t *testing.T, image []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "assembly.dll")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}
	return path
}

// TestReadAssemblyVersionRefusesIncomparableImages covers the gate holding this
// reading to what tools/prefabreader's reading of the same structure answers.
// Each refusal is of an image debug/pe reads without complaint.
func TestReadAssemblyVersionRefusesIncomparableImages(t *testing.T) {
	tests := []struct {
		name    string
		layout  func(imageLayout) imageLayout
		opts    []imageOption
		wantErr string
	}{
		{name: "well formed image"},
		{
			name:    "a section named nothing but zeros",
			opts:    []imageOption{renameSection(0, make([]byte, sectionNameSize))},
			wantErr: "",
		},
		{
			name: "a resource section spelled behind an embedded zero",
			opts: []imageOption{
				renameSection(0, []byte{'.', 'r', 's', 'r', 'c', 0, 0, 'X'}),
				shadowResource(9, 9, 9, 9),
			},
			wantErr: "padded before its end",
		},
		{
			name:    "the real resource section spelled behind an embedded zero",
			opts:    []imageOption{renameSection(1, []byte{'.', 'r', 's', 'r', 'c', 0, 0, 'X'})},
			wantErr: "padded before its end",
		},
		{
			name:    "a name padded before its end that spells no section either reading looks for",
			opts:    []imageOption{renameSection(0, []byte{'.', 't', 'e', 'x', 't', 0, 0, 'X'})},
			wantErr: "padded before its end",
		},
		{
			name:    "a section named as an offset into the COFF string table",
			opts:    []imageOption{renameSection(0, []byte("/4"))},
			wantErr: "COFF string table",
		},
		{
			name:    "an optional header narrower than sixteen directories",
			layout:  func(l imageLayout) imageLayout { l.optSize -= 8; l.directories--; return l },
			wantErr: "sixteen data directories",
		},
		{
			name:    "an optional header wider than sixteen directories",
			layout:  func(l imageLayout) imageLayout { l.optSize += 8; l.directories++; return l },
			wantErr: "sixteen data directories",
		},
		{
			name:    "no optional header at all",
			layout:  func(l imageLayout) imageLayout { l.optSize = 0; l.directories = 0; return l },
			wantErr: "declares no optional header",
		},
		{
			name:    "more sections than a signed count holds",
			layout:  func(l imageLayout) imageLayout { l.sections = maxSectionCount + 1; return l },
			wantErr: "more than a signed count holds",
		},
		{
			name:    "no DOS signature in front of the file header",
			layout:  func(l imageLayout) imageLayout { l.headerAt = 0; return l },
			wantErr: "does not begin",
		},
		{
			name:    "a resource stamped with no build",
			opts:    []imageOption{restamp(0, 0, 0, 0)},
			wantErr: "names no build",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := newImageLayout()
			if tt.layout != nil {
				layout = tt.layout(layout)
			}
			version, err := readAssemblyVersion(writeImage(t, layout.build(tt.opts...)))
			if !checkErr(t, "readAssemblyVersion", err, tt.wantErr) {
				return
			}
			if version != fixtureVersion {
				t.Errorf("readAssemblyVersion = %q, want %q", version, fixtureVersion)
			}
		})
	}
}
