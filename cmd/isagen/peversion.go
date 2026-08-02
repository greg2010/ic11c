package main

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
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

// errNoVersion reports that the assembly carries no usable version resource.
var errNoVersion = errors.New("no version resource")

// readAssemblyVersion returns the four-part file version recorded in the PE
// version resource of the named assembly, for example "0.2.6403.27689". The
// assembly's own metadata version is deliberately not used: the game stamps
// the build it shipped into the PE resource, and that is the number the
// extracted tables are keyed to.
func readAssemblyVersion(path string) (version string, err error) {
	f, err := pe.Open(path)
	if err != nil {
		return "", fmt.Errorf("open PE image %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close PE image %s: %w", path, cerr))
		}
	}()

	section := f.Section(".rsrc")
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
	if int(langEntry)+resourceDataSize > len(rsrc) {
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
// structure embedded in a VS_VERSIONINFO resource.
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
	return strings.Join(text, "."), nil
}
