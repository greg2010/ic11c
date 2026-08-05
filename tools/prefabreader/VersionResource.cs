using System;
using System.Buffers.Binary;
using System.Linq;
using System.Reflection.PortableExecutable;
using System.Text;

namespace PrefabReader;

/// <summary>
/// Decodes the four <c>VS_FIXEDFILEINFO</c> numbers from a PE's Win32 version resource. Used
/// instead of the framework's <c>FileVersionInfo</c>, which answers off managed assembly attributes.
/// </summary>
internal static class VersionResource
{
    internal const string Name = "Win32 version resource";

    // RT_VERSION = 16. Structure sizes: IMAGE_RESOURCE_DIRECTORY 16, _DIRECTORY_ENTRY 8,
    // _DATA_ENTRY 16, VS_FIXEDFILEINFO 52. All fields in this format are little-endian
    // regardless of host byte order.
    private const string Section = ".rsrc";
    private const char StringTableName = '/';
    private const char NamePadding = '\0';
    private const uint TypeVersion = 16;
    private const int DirectorySize = 16;
    private const int EntrySize = 8;
    private const int DataEntrySize = 16;
    private const uint SubdirectoryFlag = 0x80000000;
    private const uint FixedFileInfoMagic = 0xFEEF04BD;
    private const int FixedFileInfoSize = 52;

    private const int OptionalHeader32Size = 224;
    private const int OptionalHeader64Size = 240;

    /// <summary>
    /// Returns the refusal reason if <paramref name="image"/> carries no build stamp, otherwise
    /// null with <paramref name="version"/> set. Reads on-file bytes, not the loader-mapped image.
    /// </summary>
    internal static string? Read(PEReader image, out Checks.FileVersion version)
    {
        version = default;
        string? problem = Comparable(image);
        if (problem != null)
        {
            return problem;
        }
        foreach (SectionHeader header in image.PEHeaders.SectionHeaders)
        {
            if (header.Name != Section)
            {
                continue;
            }
            if (header.VirtualAddress < 0)
            {
                return $"the {Section} section is mapped at a negative address";
            }
            if (header.PointerToRawData < 0)
            {
                return $"the {Section} section is stored at a negative offset in the file";
            }
            if (header.SizeOfRawData < 0)
            {
                return $"the {Section} section declares a negative size";
            }
            // Offset zero means the file stores no bytes for this section, not that its bytes
            // start at the image's own start. Reading offset zero as written would walk the
            // resource tree over the DOS/PE headers and answer four numbers instead of refusing.
            if (header.PointerToRawData == 0)
            {
                return $"the {Section} section stores no bytes in the file";
            }

            PEMemoryBlock whole = image.GetEntireImage();
            if ((long)header.PointerToRawData + header.SizeOfRawData > whole.Length)
            {
                return $"the {Section} section runs past the end of the file";
            }
            byte[] rsrc = whole.GetContent(header.PointerToRawData, header.SizeOfRawData).ToArray();
            return FixedFileVersion(rsrc, (uint)header.VirtualAddress, out version);
        }
        return $"there is no {Section} section";
    }

    // Reports why the two readings of this build stamp (this one, and the separately fetched
    // copy it is joined against downstream by string equality) would take it off different
    // bytes, or null when they would not.
    private static string? Comparable(PEReader image) => Placed(image) ?? Spelled(image);

    // Reports why the two readings would take an image's section table from different offsets,
    // or null when they would not: this reader's framework steps the optional header by the
    // width the magic implies (224/240), not by the file header's declared width, so any other
    // declared width puts the table where only the other reading looks for it.
    private static string? Placed(PEReader image)
    {
        PEHeaders headers = image.PEHeaders;
        if (headers.PEHeader == null)
        {
            return null;
        }
        int stepped = headers.PEHeader.Magic == PEMagic.PE32
            ? OptionalHeader32Size
            : OptionalHeader64Size;
        // Framework hands this back signed; the two readings agree only when compared unsigned.
        int declared = (ushort)headers.CoffHeader.SizeOfOptionalHeader;
        return declared == stepped
            ? null
            : $"the file header declares a {declared} byte optional header where the sixteen data directories this reader steps make it {stepped}, so the two readings take the section table from different places";
    }

    // Reports why an image's section names cannot be compared as this reader compares them, or
    // null when they can: a name beginning '/' is a COFF string-table offset the other reading
    // resolves and this one does not, and this reader stops a name at its last non-zero byte
    // where the other stops at the first zero.
    private static string? Spelled(PEReader image)
    {
        foreach (SectionHeader header in image.PEHeaders.SectionHeaders)
        {
            if (header.Name.StartsWith(StringTableName))
            {
                return $"a section is named {Printable(header.Name)}, an offset into the COFF string table rather than a name";
            }
            // The framework already strips trailing padding before handing the name over, so a
            // NUL still present means a non-zero byte follows it that this reader would keep.
            if (header.Name.Contains(NamePadding))
            {
                return $"a section is named {Printable(header.Name)}, which is padded before its end and is a shorter name to a reading that stops at the padding";
            }
        }
        return null;
    }

    private static string Printable(string name)
    {
        var text = new StringBuilder(name.Length);
        foreach (char c in name)
        {
            if (c is >= ' ' and <= '~')
            {
                text.Append(c);
            }
            else
            {
                text.Append($"\\u{(int)c:x4}");
            }
        }
        return text.ToString();
    }

    /// <summary>
    /// Returns the refusal reason if the resource section carries no version, otherwise null with
    /// <paramref name="version"/> set. <paramref name="address"/> rebases the leaf into rsrc.
    /// </summary>
    internal static string? FixedFileVersion(byte[] rsrc, uint address, out Checks.FileVersion version)
    {
        version = default;
        string? problem = VersionInfo(rsrc, address, out int at, out int size);
        return problem ?? FixedFileInfo(rsrc, at, size, out version);
    }

    // Walks the resource tree's three levels -- type, name, language -- to the first version
    // leaf and answers where in rsrc that leaf's bytes sit.
    private static string? VersionInfo(byte[] rsrc, uint address, out int at, out int size)
    {
        at = 0;
        size = 0;

        string? problem = Typed(rsrc, out uint named);
        if (problem != null)
        {
            return problem;
        }
        problem = First(rsrc, named, out uint language);
        if (problem != null)
        {
            return problem;
        }
        problem = First(rsrc, language & ~SubdirectoryFlag, out uint leaf);
        if (problem != null)
        {
            return problem;
        }

        if ((leaf & SubdirectoryFlag) != 0)
        {
            return "the version resource's language level is another directory";
        }
        if ((long)leaf + DataEntrySize > rsrc.Length)
        {
            return $"the version resource's data entry at 0x{leaf:x} runs past the end of {Section}";
        }

        uint mapped = Le32(rsrc, (int)leaf);
        uint length = Le32(rsrc, (int)leaf + 4);
        if (mapped < address)
        {
            return $"the version resource is mapped at 0x{mapped:x}, in front of {Section}";
        }
        long offset = mapped - address;
        if (offset + length > rsrc.Length)
        {
            return $"the version resource extends past the end of {Section}";
        }

        at = (int)offset;
        size = (int)length;
        return null;
    }

    // A version resource is numbered, not named, so entries naming their type are walked past:
    // a named entry holds a pointer to its name in the field an id entry holds the id in.
    private static string? Typed(byte[] rsrc, out uint subtree)
    {
        subtree = 0;
        string? problem = Directory(rsrc, 0, out int entries, out int first);
        if (problem != null)
        {
            return problem;
        }
        for (int i = 0; i < entries; i++)
        {
            int at = first + (i * EntrySize);
            if (Le32(rsrc, at) == TypeVersion)
            {
                subtree = Le32(rsrc, at + 4) & ~SubdirectoryFlag;
                return null;
            }
        }
        return $"the {Section} section declares no version resource";
    }

    // Answers a directory's first entry offset with its subdirectory flag left on; which levels
    // may hold another directory differs by level, so the caller decides, not this method.
    private static string? First(byte[] rsrc, uint dir, out uint entry)
    {
        entry = 0;
        string? problem = Directory(rsrc, dir, out int entries, out int first);
        if (problem != null)
        {
            return problem;
        }
        if (entries == 0)
        {
            return $"the resource directory at 0x{dir:x} declares no entries";
        }
        entry = Le32(rsrc, first + 4);
        return null;
    }

    // Validates the header of the resource directory at dir; answers its entry count and where
    // the first entry sits.
    private static string? Directory(byte[] rsrc, uint dir, out int entries, out int first)
    {
        entries = 0;
        first = 0;
        if ((long)dir + DirectorySize > rsrc.Length)
        {
            return $"the resource directory at 0x{dir:x} runs past the end of {Section}";
        }

        entries = Le16(rsrc, (int)dir + 12) + Le16(rsrc, (int)dir + 14);
        first = (int)dir + DirectorySize;
        if (first + ((long)entries * EntrySize) > rsrc.Length)
        {
            return $"the resource directory at 0x{dir:x} declares {entries} entries, which run past the end of {Section}";
        }
        return null;
    }

    // VS_FIXEDFILEINFO is searched for rather than sat at a fixed offset, since a string table of
    // build-dependent width precedes it; the search steps 4 bytes at a time, this format's
    // alignment for every structure it defines.
    private static string? FixedFileInfo(byte[] rsrc, int at, int size, out Checks.FileVersion version)
    {
        version = default;
        int end = at + size;
        int found = -1;
        for (int i = at; i + 4 <= end; i += 4)
        {
            if (Le32(rsrc, i) == FixedFileInfoMagic)
            {
                found = i;
                break;
            }
        }
        if (found < 0)
        {
            return "the version resource carries no VS_FIXEDFILEINFO signature";
        }
        if (found + FixedFileInfoSize > end)
        {
            return "the version resource's VS_FIXEDFILEINFO is cut short";
        }

        uint high = Le32(rsrc, found + 8);
        uint low = Le32(rsrc, found + 12);
        version = new Checks.FileVersion(
            (int)(high >> 16), (int)(high & 0xFFFF), (int)(low >> 16), (int)(low & 0xFFFF));
        return null;
    }

    private static ushort Le16(byte[] bytes, int at) =>
        BinaryPrimitives.ReadUInt16LittleEndian(bytes.AsSpan(at));

    private static uint Le32(byte[] bytes, int at) =>
        BinaryPrimitives.ReadUInt32LittleEndian(bytes.AsSpan(at));
}
