using System;
using System.Buffers.Binary;
using System.Collections.Generic;
using System.IO;
using System.Reflection.PortableExecutable;
using System.Text;
using Xunit;

namespace PrefabReader.Tests;

// VersionResourceTests works on the bytes of a resource section rather than a
// file on disk, because no assembly this repository can build carries any of the
// malformed trees the walk has to refuse.
public class VersionResourceTests
{
    // The three directory levels, the data entry and the leaf laid back to back,
    // one entry apiece, which is what a compiler emits for one resource.
    private const int TypeDirectory = 0;
    private const int NameDirectory = 24;
    private const int LanguageDirectory = 48;
    private const int DataEntry = 72;
    private const int LeafAt = 88;
    private const int LeafSize = 64;
    private const int EntrySize = 8;

    // Where the VS_FIXEDFILEINFO sits, the width behind it and the signature it
    // is found by. LeafSize exceeds the two widths summed, so a row about the
    // cut-short bound declares a length of its own.
    private const int FixedFileInfoAt = 8;
    private const int FixedFileInfoSize = 52;
    private const uint FixedFileInfoMagic = 0xFEEF04BD;

    // The high bit of the field an id entry holds its id in says the field holds
    // an offset to a name instead, so no id a walk looks for can set it.
    private const uint NamedEntry = 0x80000000;

    // Not zero, so a walk that forgot to rebase the leaf's own address into the
    // section lands somewhere else rather than in the same place.
    private const uint Address = 0x2000;

    // Pairwise distinct: neither the halves within a word nor the two words can
    // be swapped without changing what comes out.
    private static readonly Checks.FileVersion Stamped = new(5, 6, 7, 8);

    // A section header is forty bytes, and the three offsets below are from its
    // start.
    private const int SectionHeaderSize = 40;
    private const int VirtualAddress = 12;
    private const int SizeOfRawData = 16;
    private const int PointerToRawData = 20;

    // A leading slash means the name is kept in the COFF string table and this
    // is an offset into it.
    private const string StringTableName = "/4";

    // A name spelled into its own padding. A reading stopping at the first zero
    // sees the resource section's name here and this reader does not, which is
    // the whole of what makes the two read different sections.
    private const string PaddedName = ".rsrc\0\0X";

    // Refusals are written out rather than read off a terminal, so the bytes no
    // name holds travel escaped.
    private const string PaddedSpelling = @".rsrc\u0000\u0000X";

    // Both kinds of wrong name at once. The refusal names it as the offset,
    // being the more particular of the two.
    private const string PaddedStringTableName = "/4\0\0\0\0\0X";
    private const string PaddedStringTableSpelling = @"/4\u0000\u0000\u0000\u0000\u0000X";

    // What this suite's own project file stamps its PE with: a resource a build
    // wrote rather than one a fixture invented.
    private static readonly Checks.FileVersion Built = new(1, 2, 3, 4);

    // All three are from the start of the PE signature.
    private const int NumberOfSections = 6;
    private const int SizeOfOptionalHeader = 20;
    private const int OptionalHeaderMagic = 24;

    // A file that does not begin MZ carries no PE signature, so its file header
    // begins the file and each field above sits four bytes earlier in it.
    private const int PESignatureSize = 4;
    private const int FileHeaderSize = 20;

    // The two magics an optional header can carry, the widths sixteen data
    // directories give each, and the ROM image magic that is neither -- a shape
    // the format describes rather than a value picked for being unused.
    private const ushort PE32 = 0x10b;
    private const ushort PE32Plus = 0x20b;
    private const ushort RomImage = 0x107;
    private const int PE32Width = 224;
    private const int PE32PlusWidth = 240;

    // The largest count both readings enumerate, one reading the field signed.
    private const int SignedSectionCount = 0x7FFF;

    // Any value but the DOS signature's own bytes sends the file down the
    // object-file path, and this is a real machine.
    private const ushort I386 = 0x014c;

    private const string ResourceSection = ".rsrc";

    [Fact]
    public void FixedFileVersionReadsTheFourNumbersOfAWellFormedResource()
    {
        Assert.Null(VersionResource.FixedFileVersion(Fixture(), Address, out Checks.FileVersion version));
        Assert.Equal(Stamped, version);
    }

    // Each row perturbs one field into a claim the bytes do not bear out. The
    // walk must refuse rather than answer four numbers off wherever it pointed.
    [Fact]
    public void FixedFileVersionRefusesEveryTreeItCannotWalk()
    {
        (string Name, Action<byte[]> Break, string Want)[] cases =
        {
            (
                "a section with no version resource in it",
                rsrc => Entry(rsrc, TypeDirectory + 16, 17, NameDirectory | 0x80000000),
                "the .rsrc section declares no version resource"),
            (
                "a version resource under no name",
                rsrc => Counts(rsrc, NameDirectory, 0, 0),
                $"the resource directory at 0x{NameDirectory:x} declares no entries"),
            (
                "a version resource under no language",
                rsrc => Counts(rsrc, LanguageDirectory, 0, 0),
                $"the resource directory at 0x{LanguageDirectory:x} declares no entries"),
            (
                "a language level that is another directory",
                rsrc => Entry(rsrc, LanguageDirectory + 16, 1033, DataEntry | 0x80000000),
                "the version resource's language level is another directory"),
            (
                "a directory placed past the end of the section",
                rsrc => Entry(rsrc, TypeDirectory + 16, 16, 0x8000BEEF),
                "the resource directory at 0xbeef runs past the end of .rsrc"),
            (
                "a directory claiming more entries than the section holds",
                rsrc => Counts(rsrc, TypeDirectory, 0, 4096),
                "the resource directory at 0x0 declares 4096 entries, which run past the end of .rsrc"),
            (
                "a data entry placed past the end of the section",
                rsrc => Entry(rsrc, LanguageDirectory + 16, 1033, (uint)LeafAt + LeafSize - 4),
                $"the version resource's data entry at 0x{LeafAt + LeafSize - 4:x} runs past the end of .rsrc"),
            (
                "a leaf mapped in front of the section",
                rsrc => Le32(rsrc, DataEntry, Address - 1),
                $"the version resource is mapped at 0x{Address - 1:x}, in front of .rsrc"),
            (
                "a leaf running past the end of the section",
                rsrc => Le32(rsrc, DataEntry + 4, LeafSize + 1),
                "the version resource extends past the end of .rsrc"),
            (
                "a leaf carrying no VS_FIXEDFILEINFO",
                rsrc => Le32(rsrc, LeafAt + 8, 0),
                "the version resource carries no VS_FIXEDFILEINFO signature"),
            (
                "a VS_FIXEDFILEINFO the leaf ends one byte in front of the end of",
                rsrc => Le32(rsrc, DataEntry + 4, FixedFileInfoAt + FixedFileInfoSize - 1),
                "the version resource's VS_FIXEDFILEINFO is cut short"),
        };

        var failures = new List<string>();
        foreach ((string name, Action<byte[]> @break, string want) in cases)
        {
            byte[] rsrc = Fixture();
            @break(rsrc);

            string? got = VersionResource.FixedFileVersion(rsrc, Address, out Checks.FileVersion version);
            if (got != want)
            {
                failures.Add($"{name}: got {got ?? "no problem"}, want {want}");
            }
            else if (version != default)
            {
                failures.Add($"{name}: answered {version} beside the problem");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Every row is a tree standing one step from a refusal. Each helper below
    // says which line it stands against.
    [Fact]
    public void FixedFileVersionTakesTheStampOffEveryTreeItCanWalk()
    {
        (string Name, Func<byte[]> Tree)[] cases =
        {
            ("a signature spelled off the alignment in front of the structure", Misaligned),
            ("an entry naming its type in front of the one numbering it", () => Fixture(1)),
            ("a version resource under a name rather than an id", () => Named(NameDirectory)),
            ("a version resource in a language named rather than numbered", () => Named(LanguageDirectory)),
            ("a structure whose last byte is the leaf's last byte", FlushWithTheLeaf),
            ("a leaf beginning where the section begins", Whole),
        };

        var failures = new List<string>();
        foreach ((string name, Func<byte[]> tree) in cases)
        {
            string? got = VersionResource.FixedFileVersion(tree(), Address, out Checks.FileVersion version);
            if (got != null)
            {
                failures.Add($"{name}: refused with {got}");
            }
            else if (version != Stamped)
            {
                failures.Add($"{name}: answered {version}, want {Stamped}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Misaligned spells the signature once more one byte into the leaf. Every
    // structure the format describes sits on a four-byte boundary, but the
    // string table the search steps over may spell those bytes anywhere, and a
    // one-byte stride answers off a spelling rather than a structure.
    private static byte[] Misaligned()
    {
        byte[] rsrc = Fixture();
        Le32(rsrc, LeafAt + 1, FixedFileInfoMagic);
        return rsrc;
    }

    // Named holds the directory's entry under a name rather than an id. A
    // directory counts what it names and what it numbers in two separate
    // fields, and a walk reading only the second reaches neither.
    private static byte[] Named(int dir)
    {
        byte[] rsrc = Fixture();
        Counts(rsrc, dir, 1, 0);
        Le32(rsrc, dir + 16, NamedEntry);
        return rsrc;
    }

    // FlushWithTheLeaf sits exactly on the bound: the structure's last byte is
    // the last byte the leaf declares, and the fixture sits on neither bound.
    private static byte[] FlushWithTheLeaf()
    {
        byte[] rsrc = Fixture();
        Le32(rsrc, DataEntry + 4, FixedFileInfoAt + FixedFileInfoSize);
        return rsrc;
    }

    // Whole is the other bound: the one shape a leaf takes that rebases to the
    // front of the section rather than past it.
    private static byte[] Whole()
    {
        byte[] rsrc = Fixture();
        Le32(rsrc, DataEntry, Address);
        Le32(rsrc, DataEntry + 4, (uint)rsrc.Length);
        return rsrc;
    }

    // Anchors the tables below: every row there is this image with one field
    // rewritten, so a row that refuses says something only if this does not.
    [Fact]
    public void ReadTakesTheStampOffAWholeImage()
    {
        Assert.Null(Read(Image(), out Checks.FileVersion version));
        Assert.Equal(Built, version);
    }

    // A name is not unique in a section table and nothing in the format makes it
    // so. Both readings take the first of that name in table order; a reader
    // taking any other reads bytes the other never looked at.
    [Fact]
    public void ReadTakesTheFirstSectionSpelledForTheResourceTree()
    {
        byte[] image = Image();
        Name(image, Behind(image), ResourceSection);

        Assert.Null(Read(image, out Checks.FileVersion version));
        Assert.Equal(Built, version);
    }

    // Zero is not a small offset: a header pointing at file offset zero declares
    // the file stores none of the section's bytes, and a walk that seeks there
    // reads the DOS and PE headers as though they were the tree. The name rows
    // are asked of the whole table, which the rows behind .rsrc are what say.
    [Fact]
    public void ReadRefusesEverySectionHeaderItCannotTake()
    {
        (string Name, Action<byte[]> Break, string Want)[] cases =
        {
            (
                "a resource section the file stores no bytes of",
                image => Le32(image, Resource(image) + PointerToRawData, 0),
                "the .rsrc section stores no bytes in the file"),
            (
                "a resource section stored at a negative file offset",
                image => Le32(image, Resource(image) + PointerToRawData, 0x80000000),
                "the .rsrc section is stored at a negative offset in the file"),
            (
                "a resource section mapped at a negative address",
                image => Le32(image, Resource(image) + VirtualAddress, 0x80000000),
                "the .rsrc section is mapped at a negative address"),
            (
                "a resource section declaring a negative size",
                image => Le32(image, Resource(image) + SizeOfRawData, 0x80000000),
                "the .rsrc section declares a negative size"),
            (
                "a resource section running past the end of the file",
                image => Le32(image, Resource(image) + SizeOfRawData, 0x40000000),
                "the .rsrc section runs past the end of the file"),
            (
                "a resource section spelling its name through the string table",
                image => Name(image, Resource(image), StringTableName),
                $"a section is named {StringTableName}, an offset into the COFF string table rather than a name"),
            (
                "a section behind a whole one spelling its name that way",
                image => Name(image, Behind(image), StringTableName),
                $"a section is named {StringTableName}, an offset into the COFF string table rather than a name"),
            (
                "a resource section spelling its name into its own padding",
                image => Name(image, Resource(image), PaddedName),
                $"a section is named {PaddedSpelling}, which is padded before its end and is a shorter name to a reading that stops at the padding"),
            (
                "a section ahead of a whole one spelling that name into its padding",
                image => Name(image, Ahead(image), PaddedName),
                $"a section is named {PaddedSpelling}, which is padded before its end and is a shorter name to a reading that stops at the padding"),
            (
                "a section behind a whole one spelling that name into its padding",
                image => Name(image, Behind(image), PaddedName),
                $"a section is named {PaddedSpelling}, which is padded before its end and is a shorter name to a reading that stops at the padding"),
            (
                "a string table offset with padding behind it",
                image => Name(image, Ahead(image), PaddedStringTableName),
                $"a section is named {PaddedStringTableSpelling}, an offset into the COFF string table rather than a name"),
        };

        var failures = new List<string>();
        foreach ((string name, Action<byte[]> @break, string want) in cases)
        {
            byte[] image = Image();
            @break(image);

            string? got = Read(image, out Checks.FileVersion version);
            if (got != want)
            {
                failures.Add($"{name}: got {got ?? "no problem"}, want {want}");
            }
            else if (version != default)
            {
                failures.Add($"{name}: answered {version} beside the problem");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The framework ignores the declared optional-header width: it steps by the
    // width sixteen data directories give the magic and takes the table from
    // there. The reading this is joined against steps the declared width, so a
    // row is an image whose table the two would find in different places.
    [Fact]
    public void ReadRefusesAnImageTheTwoReadingsWouldTakeTheSectionTableFromTwoPlacesIn()
    {
        (string Name, int Declared, Action<byte[]>? Also)[] cases =
        {
            ("a width one data directory short of sixteen", 216, null),
            ("a width one data directory past sixteen", 232, null),
            ("a file header declaring no optional header at all", 0, null),
            ("a width with its sign bit set, which one reading takes unsigned", 0x8000, null),
            ("a width that is wrong beside a name this reader cannot spell",
                216, image => Name(image, Resource(image), PaddedName)),
            ("the wider shape's magic beside the narrower shape's width",
                PE32Width, image => Le16(image, Signature(image) + OptionalHeaderMagic, PE32Plus)),
            ("the narrower shape's magic beside the wider shape's width",
                PE32PlusWidth, image => Le16(image, Signature(image) + OptionalHeaderMagic, PE32)),
        };

        var failures = new List<string>();
        foreach ((string name, int declared, Action<byte[]>? also) in cases)
        {
            byte[] image = Image();
            also?.Invoke(image);
            Le16(image, Signature(image) + SizeOfOptionalHeader, (ushort)declared);

            int stepped = Stepped(image);
            string want = $"the file header declares a {declared} byte optional header where the sixteen data directories this reader steps make it {stepped}, so the two readings take the section table from different places";
            string? got = Read(image, out Checks.FileVersion version);
            if (got != want)
            {
                failures.Add($"{name}: got {got ?? "no problem"}, want {want}");
            }
            else if (version != default)
            {
                failures.Add($"{name}: answered {version} beside the problem");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The three tests below pin framework behaviour this reader rests on rather
    // than implements, so they guard a change of framework, not a malformed
    // input. Each holds the type raised and not the sentence, the sentence being
    // framework prose, and each asks a bound so that refusing all is not a pass.
    // The object-file one holds the outcome instead, for the reason it gives.

    [Fact]
    public void TheFrameworkTakesTheSectionCountUpToTheSignBitAndRefusesPastIt()
    {
        byte[] largest = Counted(SignedSectionCount);
        Assert.Null(Refused(largest));
        Assert.Equal(SignedSectionCount, Enumerated(largest));
        Assert.IsType<BadImageFormatException>(Refused(Counted(SignedSectionCount + 1)));
    }

    [Fact]
    public void TheFrameworkReadsAnObjectFileOnlyWhereItDeclaresNoOptionalHeader()
    {
        Assert.Null(Read(ObjectFile(0), out Checks.FileVersion version));
        Assert.Equal(Stamped, version);
        Assert.Equal(default, Answered(ObjectFile(PE32Width)));
    }

    // The version a reading answers, or none where it refuses. .NET 9 refuses by
    // finding no resource section, .NET 10 by raising.
    private static Checks.FileVersion Answered(byte[] image)
    {
        try
        {
            return Read(image, out Checks.FileVersion version) is null ? version : default;
        }
        catch (BadImageFormatException)
        {
            return default;
        }
    }

    [Fact]
    public void TheFrameworkRefusesAnOptionalHeaderDeclaringNeitherMagic()
    {
        byte[] image = Image();
        Le16(image, Signature(image) + OptionalHeaderMagic, RomImage);
        Assert.IsType<BadImageFormatException>(Refused(image));
    }

    // The reach is Read's own, because that is where the reader touches the
    // headers and where Declarations.Stamp stands to catch what the touch raises.
    private static Exception? Refused(byte[] image)
    {
        try
        {
            Read(image, out _);
        }
        catch (Exception failure)
        {
            return failure;
        }
        return null;
    }

    // The length both readings have to enumerate alike.
    private static int Enumerated(byte[] image)
    {
        using var pe = new PEReader(new MemoryStream(image, writable: false));
        return pe.PEHeaders.SectionHeaders.Length;
    }

    // Room is appended for the whole table so the count is the only thing a row
    // rewrites. The framework refuses a table past the end of the file with the
    // same sentence it refuses a count with its sign bit set, so an image left
    // at its own length would pass unchanged through an unsigned framework.
    private static byte[] Counted(int sections)
    {
        byte[] pristine = Image();
        var image = new byte[pristine.Length + (sections * SectionHeaderSize)];
        Array.Copy(pristine, image, pristine.Length);
        Le16(image, Signature(image) + NumberOfSections, (ushort)sections);
        return image;
    }

    // ObjectFile does not begin MZ, and lays its section table at the end of its
    // file header, which is where both readings take the table of such a file.
    // width is what the file header declares an optional header between them.
    private static byte[] ObjectFile(int width)
    {
        byte[] rsrc = Fixture();
        int table = FileHeaderSize + width;
        int raw = table + SectionHeaderSize;
        var image = new byte[raw + rsrc.Length];

        // The machine begins the file header, which begins the file.
        Le16(image, 0, I386);
        Le16(image, NumberOfSections - PESignatureSize, 1);
        Le16(image, SizeOfOptionalHeader - PESignatureSize, (ushort)width);

        Encoding.ASCII.GetBytes(ResourceSection, 0, ResourceSection.Length, image, table);
        Le32(image, table + VirtualAddress, Address);
        Le32(image, table + SizeOfRawData, (uint)rsrc.Length);
        Le32(image, table + PointerToRawData, (uint)raw);
        Array.Copy(rsrc, 0, image, raw, rsrc.Length);
        return image;
    }

    // A function of the magic alone. The framework refuses to read headers out
    // of any other magic, so the two shapes are all a row can be built on.
    private static int Stepped(byte[] image) =>
        BinaryPrimitives.ReadUInt16LittleEndian(image.AsSpan(Signature(image) + OptionalHeaderMagic)) == PE32
            ? PE32Width
            : PE32PlusWidth;

    // A row rewrites bytes and never a file, so the suite's own assembly is read
    // rather than copied.
    private static string? Read(byte[] image, out Checks.FileVersion version)
    {
        using var pe = new PEReader(new MemoryStream(image, writable: false));
        return VersionResource.Read(pe, out version);
    }

    // The one PE on hand carrying a version resource some build stamped.
    private static byte[] Image() =>
        File.ReadAllBytes(typeof(VersionResourceTests).Assembly.Location);

    // Each is taken from where the resource section is rather than from table
    // order, because which side of it a perturbed header falls on is the whole
    // of what a row asserts: a guard reading only as far as the header it
    // matches passes every row ahead and fails every row behind.
    private static int Resource(byte[] image) => Header(image, Index(image));

    private static int Ahead(byte[] image) => Header(image, Index(image) - 1);

    private static int Behind(byte[] image) => Header(image, Index(image) + 1);

    private static int Index(byte[] image)
    {
        for (int i = 0; i < Sections(image); i++)
        {
            if (Encoding.ASCII.GetString(image, Header(image, i), 8).TrimEnd('\0') == ResourceSection)
            {
                return i;
            }
        }
        Assert.Fail("this suite's own assembly carries no resource section for these rows to be placed around");
        return -1;
    }

    // The framework reads a header without saying where from, so the table is
    // located by walking the declared sizes: the PE signature at the offset the
    // DOS header ends with, four bytes of it, then the twenty-byte COFF header
    // whose last fields give the optional header the table follows.
    private static int Header(byte[] image, int index)
    {
        int sections = Sections(image);
        Assert.True(
            index >= 0 && index < sections,
            $"this suite's own assembly lays its resource section at the edge of its {sections} sections, so a row needing a section on one side of it has none to perturb");
        int pe = Signature(image);
        int optional = BinaryPrimitives.ReadUInt16LittleEndian(image.AsSpan(pe + 20));
        return pe + 24 + optional + (index * SectionHeaderSize);
    }

    private static int Sections(byte[] image) =>
        BinaryPrimitives.ReadUInt16LittleEndian(image.AsSpan(Signature(image) + NumberOfSections));

    private static int Signature(byte[] image) =>
        BinaryPrimitives.ReadInt32LittleEndian(image.AsSpan(0x3c));

    private static void Name(byte[] image, int header, string name)
    {
        Array.Clear(image, header, 8);
        Encoding.ASCII.GetBytes(name, 0, name.Length, image, header);
    }

    // A resource section carrying one version resource, mapped at Address the
    // way a real section is.
    private static byte[] Fixture() => Fixture(0);

    // ahead is how many entries naming their type sit in front of the one
    // numbering it, each moving every level below the type level down an entry.
    private static byte[] Fixture(int ahead)
    {
        int shift = ahead * EntrySize;
        int name = NameDirectory + shift;
        int language = LanguageDirectory + shift;
        int data = DataEntry + shift;
        int leaf = LeafAt + shift;

        var rsrc = new byte[leaf + LeafSize];
        Counts(rsrc, TypeDirectory, (ushort)ahead, 1);
        for (int i = 0; i < ahead; i++)
        {
            // Placed past the end of the section, so a walk reaching this entry
            // refuses rather than answering off whatever it points at.
            Entry(rsrc, TypeDirectory + 16 + (i * EntrySize), NamedEntry, 0x8000BEEF);
        }
        Entry(rsrc, TypeDirectory + 16 + shift, 16, (uint)name | 0x80000000);
        Counts(rsrc, name, 0, 1);
        Entry(rsrc, name + 16, 1, (uint)language | 0x80000000);
        Counts(rsrc, language, 0, 1);
        Entry(rsrc, language + 16, 1033, (uint)data);

        Le32(rsrc, data, (uint)(Address + leaf));
        Le32(rsrc, data + 4, LeafSize);

        // Past the front of the leaf, so a walk taking the first word for the
        // signature fails here as it would on a real resource.
        int info = leaf + FixedFileInfoAt;
        Le32(rsrc, info, FixedFileInfoMagic);
        Le32(rsrc, info + 8, (uint)((Stamped.Major << 16) | Stamped.Minor));
        Le32(rsrc, info + 12, (uint)((Stamped.Build << 16) | Stamped.Revision));
        return rsrc;
    }

    private static void Counts(byte[] rsrc, int at, ushort named, ushort ids)
    {
        BinaryPrimitives.WriteUInt16LittleEndian(rsrc.AsSpan(at + 12), named);
        BinaryPrimitives.WriteUInt16LittleEndian(rsrc.AsSpan(at + 14), ids);
    }

    private static void Entry(byte[] rsrc, int at, uint id, uint offset)
    {
        Le32(rsrc, at, id);
        Le32(rsrc, at + 4, offset);
    }

    private static void Le32(byte[] rsrc, int at, uint value) =>
        BinaryPrimitives.WriteUInt32LittleEndian(rsrc.AsSpan(at), value);

    private static void Le16(byte[] image, int at, ushort value) =>
        BinaryPrimitives.WriteUInt16LittleEndian(image.AsSpan(at), value);
}
