package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// The subset of the Windows resource format needed to reach VS_FIXEDFILEINFO.
const (
	rtVersion            = 16
	resourceDirSize      = 16
	resourceEntrySize    = 8
	resourceDataSize     = 16
	resourceSubdirFlag   = 0x80000000
	fixedFileInfoMagic   = 0xFEEF04BD
	fixedFileInfoMinSize = 52
)

// The subset of the PE headers [checkComparableImage] reads: where an image
// says its own headers begin, how wide the structures between them are, and the
// two spellings of a section name that mean something other than their bytes.
const (
	dosSignature      = "MZ"
	dosHeaderSize     = 0x40
	lfanewAt          = 0x3c
	peSignatureSize   = 4
	fileHeaderSize    = 20
	sectionHeaderSize = 40
	sectionNameSize   = 8

	// The widths a PE optional header has when it carries the sixteen data
	// directories a linker emits, one per magic.
	optionalHeader32Size = 224
	optionalHeader64Size = 240

	// The largest section count both readings enumerate, one of them reading
	// the field as signed.
	maxSectionCount = 0x7FFF

	// The byte a section header spells a name it does not hold with.
	stringTableName = '/'

	// The stamp of a build somebody stripped the version out of.
	unstampedVersion = "0.0.0.0"
)

// errNoVersion reports that the assembly carries no usable version resource.
var errNoVersion = errors.New("no version resource")

// errIncomparable reports an assembly whose headers the two readings of its
// build stamp would not decode alike. Each side refuses whatever the two
// cannot agree about, so for any assembly either both answer the same
// version or at least one refuses. See [checkComparableImage].
var errIncomparable = errors.New("the two readings of this build stamp would not agree")

// readAssemblyVersion returns the four-part file version recorded in the PE
// version resource of the named assembly, for example "0.2.6403.27689". The
// assembly's own metadata version is deliberately not used: the game stamps
// the build it shipped, and that is the number the extracted tables are keyed to.
func readAssemblyVersion(path string) (version string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open assembly %s: %w", path, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close assembly %s: %w", path, cerr))
		}
	}()

	image, err := pe.NewFile(file)
	if err != nil {
		return "", fmt.Errorf("open PE image %s: %w", path, err)
	}
	if err := checkComparableImage(file, image); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}

	section := image.Section(".rsrc")
	if section == nil {
		return "", fmt.Errorf("%s: %w: no .rsrc section", path, errNoVersion)
	}
	data, err := section.Data()
	if err != nil {
		return "", fmt.Errorf("read .rsrc of %s: %w", path, err)
	}

	// Entries in the version resource are addressed by RVA, which the .rsrc
	// section's own virtual address rebases into the section data.
	info, err := findVersionInfo(data, section.VirtualAddress)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	version, err = fixedFileVersion(info)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return version, nil
}

// checkComparableImage refuses an image whose headers this reading and the
// reading tools/prefabreader takes of the same structure would not decode alike.
func checkComparableImage(r io.ReaderAt, image *pe.File) error {
	base, err := fileHeaderOffset(r)
	if err != nil {
		return err
	}
	if err := checkOptionalHeaderWidth(image); err != nil {
		return err
	}
	// debug/pe reads the section count unsigned; the managed reader reads it
	// signed and refuses a negative one, so past this bound one reading has a
	// section table and the other has none.
	if image.NumberOfSections > maxSectionCount {
		return fmt.Errorf("the file header declares %d sections, more than a signed count holds: %w",
			image.NumberOfSections, errIncomparable)
	}
	table := base + fileHeaderSize + int64(image.SizeOfOptionalHeader)
	return checkSectionNames(r, table, int(image.NumberOfSections))
}

// fileHeaderOffset reports where the COFF file header begins, taken from the
// DOS header's lfanew field. The signature is checked, not trusted, since its
// absence is what tells debug/pe to read the file header at zero instead --
// leaving lfanew some unrelated word [checkSectionNames] would walk from.
func fileHeaderOffset(r io.ReaderAt) (int64, error) {
	var dos [dosHeaderSize]byte
	if _, err := io.ReadFull(io.NewSectionReader(r, 0, int64(len(dos))), dos[:]); err != nil {
		return 0, fmt.Errorf("read the DOS header: %w", err)
	}
	if string(dos[:len(dosSignature)]) != dosSignature {
		return 0, fmt.Errorf("the image does not begin %q, so the offset this reading takes its headers from is not where debug/pe read them: %w",
			dosSignature, errIncomparable)
	}
	return int64(binary.LittleEndian.Uint32(dos[lfanewAt:])) + peSignatureSize, nil
}

// checkOptionalHeaderWidth holds the optional header to the width that puts
// the section table where both readings look for it: debug/pe walks by the
// declared width, and the managed reader walks by the width sixteen data
// directories imply, so any other width parses the table at two different offsets.
func checkOptionalHeaderWidth(image *pe.File) error {
	var want uint16
	switch image.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		want = optionalHeader32Size
	case *pe.OptionalHeader64:
		want = optionalHeader64Size
	default:
		return fmt.Errorf("the file header declares no optional header, so one reading takes the section table %d bytes past it and the other from past an optional header it walks anyway: %w",
			fileHeaderSize, errIncomparable)
	}
	if got := image.SizeOfOptionalHeader; got != want {
		return fmt.Errorf("the file header declares a %d byte optional header where sixteen data directories make it %d, so the two readings take the section table from different places: %w",
			got, want, errIncomparable)
	}
	return nil
}

// checkSectionNames refuses an image any of whose section names the two
// readings spell differently.
func checkSectionNames(r io.ReaderAt, at int64, count int) error {
	table := make([]byte, count*sectionHeaderSize)
	if _, err := io.ReadFull(io.NewSectionReader(r, at, int64(len(table))), table); err != nil {
		return fmt.Errorf("read the section table: %w", err)
	}
	for i := range count {
		name := table[i*sectionHeaderSize:][:sectionNameSize]
		switch {
		// A leading slash is a COFF string table offset, which debug/pe
		// resolves and the managed reader takes at face value.
		case name[0] == stringTableName:
			return fmt.Errorf("section %d is named %s, an offset into the COFF string table rather than a name: %w",
				i, strconv.Quote(string(name)), errIncomparable)
		// A zero ends the name: debug/pe stops at the first zero and the
		// managed reader at the last non-zero byte, so the two disagree
		// wherever a name is zero-padded and then followed by a stray
		// non-zero byte.
		case bytes.IndexByte(bytes.TrimRight(name, "\x00"), 0) >= 0:
			return fmt.Errorf("section %d is named %s, which is padded before its end and is a shorter name to a reading that stops at the padding: %w",
				i, strconv.Quote(string(name)), errIncomparable)
		}
	}
	return nil
}

// findVersionInfo walks the three levels of the resource directory tree,
// type then name then language, and returns the bytes of the first
// RT_VERSION leaf.
func findVersionInfo(rsrc []byte, virtualAddress uint32) ([]byte, error) {
	typeEntry, err := resourceEntry(rsrc, 0, rtVersion)
	if err != nil {
		return nil, fmt.Errorf("locate RT_VERSION: %w", err)
	}
	nameEntry, err := firstEntry(rsrc, typeEntry)
	if err != nil {
		return nil, fmt.Errorf("locate version resource name: %w", err)
	}
	langEntry, err := firstEntry(rsrc, nameEntry&^resourceSubdirFlag)
	if err != nil {
		return nil, fmt.Errorf("locate version resource language: %w", err)
	}
	if langEntry&resourceSubdirFlag != 0 {
		return nil, fmt.Errorf("version resource language level is a subdirectory: %w", errNoVersion)
	}
	if uint64(langEntry)+resourceDataSize > uint64(len(rsrc)) {
		return nil, fmt.Errorf("version resource data entry out of range: %w", errNoVersion)
	}
	dataRVA := binary.LittleEndian.Uint32(rsrc[langEntry:])
	size := binary.LittleEndian.Uint32(rsrc[langEntry+4:])
	if dataRVA < virtualAddress {
		return nil, fmt.Errorf("version resource RVA %#x precedes .rsrc: %w", dataRVA, errNoVersion)
	}
	offset := dataRVA - virtualAddress
	if uint64(offset)+uint64(size) > uint64(len(rsrc)) {
		return nil, fmt.Errorf("version resource extends past .rsrc: %w", errNoVersion)
	}
	return rsrc[offset : offset+size], nil
}

// resourceEntry returns the offset field of the directory entry at dir whose
// id matches want. Named entries are skipped, since RT_VERSION is an id.
func resourceEntry(rsrc []byte, dir uint32, want uint32) (uint32, error) {
	named, ids, base, err := directoryEntries(rsrc, dir)
	if err != nil {
		return 0, err
	}
	for i := uint32(0); i < named+ids; i++ {
		at := base + i*resourceEntrySize
		id := binary.LittleEndian.Uint32(rsrc[at:])
		if id == want {
			return binary.LittleEndian.Uint32(rsrc[at+4:]) &^ resourceSubdirFlag, nil
		}
	}
	return 0, fmt.Errorf("resource id %d: %w", want, errNoVersion)
}

// firstEntry returns the offset field of the first entry of the directory at
// dir, subdirectory flag included. Whether an entry may be a subdirectory
// differs by level, so masking the flag off here would discard what the caller
// has to check.
func firstEntry(rsrc []byte, dir uint32) (uint32, error) {
	named, ids, base, err := directoryEntries(rsrc, dir)
	if err != nil {
		return 0, err
	}
	if named+ids == 0 {
		return 0, fmt.Errorf("empty resource directory at %#x: %w", dir, errNoVersion)
	}
	return binary.LittleEndian.Uint32(rsrc[base+4:]), nil
}

// directoryEntries validates the resource directory header at dir and reports
// its named and id entry counts along with the offset of its first entry.
func directoryEntries(rsrc []byte, dir uint32) (named, ids, base uint32, err error) {
	if uint64(dir)+resourceDirSize > uint64(len(rsrc)) {
		return 0, 0, 0, fmt.Errorf("resource directory at %#x out of range: %w", dir, errNoVersion)
	}
	named = uint32(binary.LittleEndian.Uint16(rsrc[dir+12:]))
	ids = uint32(binary.LittleEndian.Uint16(rsrc[dir+14:]))
	base = dir + resourceDirSize
	if uint64(base)+uint64(named+ids)*resourceEntrySize > uint64(len(rsrc)) {
		return 0, 0, 0, fmt.Errorf("resource directory at %#x has %d entries past end of .rsrc: %w", dir, named+ids, errNoVersion)
	}
	return named, ids, base, nil
}

// fixedFileVersion extracts the dotted file version from the VS_FIXEDFILEINFO
// structure embedded in a VS_VERSIONINFO resource. A structure stamped four
// zeros is refused: it is the one shape that decodes and still names no
// build, which would key the extracted tables to a string that joins against
// any other stripped build as readily as against itself.
func fixedFileVersion(info []byte) (string, error) {
	at := -1
	for i := 0; i+4 <= len(info); i += 4 {
		if binary.LittleEndian.Uint32(info[i:]) == fixedFileInfoMagic {
			at = i
			break
		}
	}
	if at < 0 {
		return "", fmt.Errorf("VS_FIXEDFILEINFO signature: %w", errNoVersion)
	}
	if at+fixedFileInfoMinSize > len(info) {
		return "", fmt.Errorf("VS_FIXEDFILEINFO truncated: %w", errNoVersion)
	}
	ms := binary.LittleEndian.Uint32(info[at+8:])
	ls := binary.LittleEndian.Uint32(info[at+12:])
	parts := []uint32{ms >> 16, ms & 0xFFFF, ls >> 16, ls & 0xFFFF}
	text := make([]string, len(parts))
	for i, p := range parts {
		text[i] = fmt.Sprint(p)
	}
	version := strings.Join(text, ".")
	if version == unstampedVersion {
		return "", fmt.Errorf("VS_FIXEDFILEINFO is stamped %s, which names no build: %w", unstampedVersion, errNoVersion)
	}
	return version, nil
}
