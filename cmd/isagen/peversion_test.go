package main

import (
	"encoding/binary"
	"path/filepath"
	"testing"
)

// Offsets within the resource fixture. The three directory levels and the data
// entry are laid out back to back, each holding a single entry, which is the
// shape readAssemblyVersion walks.
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
