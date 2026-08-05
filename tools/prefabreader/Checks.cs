using System;
using System.Collections.Generic;
using System.Collections.Immutable;
using System.IO;
using System.Linq;
using AssetsTools.NET;

namespace PrefabReader;

/// <summary>
/// What this reader expects the game to look like, and what it decides when the game stops
/// looking like it — as a function of names, types and counts, never a serialized file.
/// </summary>
internal static class Checks
{
    // Stamped into the roster because the roster is joined downstream, by string equality,
    // against a version resource read off a separately fetched copy of this same file.
    internal const string AssemblyFile = "Assembly-CSharp.dll";

    // Also where MonoCecil resolves every script type's base and field types from, not only
    // AssemblyFile itself. A dependency missing from this directory surfaces as MonoCecil's own
    // AssemblyResolutionException naming the assembly it could not resolve.
    internal const string ManagedDirectory = "Managed";

    // Not the only depot file this reader opens: an asset's driving class is looked up through
    // the script-type table, which does not point exclusively here. Every file this constant or
    // that lookup names is held to SerializedFileProblem before the walk reads through it.
    internal const string ResourcesFile = "resources.assets";

    // WorldManagerClass is the namespace-qualified name of the class the prefab roster hangs off.
    internal const string WorldManagerClass = "WorldManager";

    // Read off the assembly directly rather than off the serialized layout, because the
    // serialized layout answers a narrower question than this reader asks of it.
    internal const string ThingClass = "Assets.Scripts.Objects.Thing";
    internal const string DeviceClass = "Assets.Scripts.Objects.Pipes.Device";

    // PowerField is the draw the base logic surface gates RequiredPower on.
    internal const string PowerField = "UsedPower";

    internal const string PrefabNameField = "PrefabName";
    internal const string PrefabHashField = "PrefabHash";
    internal const string SlotsField = "Slots";
    internal const string RosterField = "SourcePrefabs";

    // The game serializes the Slot.Class ordinal under a name that says nothing about what it
    // holds.
    internal const string SlotClassField = "Type";

    // The engine's own names, which move with the Unity version rather than with the game: the
    // child a serialized array hangs its entries off, the two halves of a pointer to another
    // asset, and the two halves of the class name a MonoScript stands for.
    internal const string ArrayField = "Array";
    internal const string FileIdField = "m_FileID";
    internal const string PathIdField = "m_PathID";
    internal const string NamespaceField = "m_Namespace";
    internal const string ClassNameField = "m_ClassName";

    // The CLR types the state flags and the draw have to keep for the values this reader takes
    // out of them to mean what it reads them as.
    internal const string FlagFieldType = "System.Boolean";
    internal const string PowerFieldType = "System.Single";

    /// <summary>One instance field of a class as the assembly declares it.</summary>
    /// <param name="Type">The field's full CLR type name.</param>
    /// <param name="Serialized">Whether Unity writes this field into a serialized file at all.</param>
    internal readonly record struct DeclaredField(string Type, bool Serialized);

    // Unity writes a bool as a single byte, and the synthesised template names that byte UInt8,
    // so this is the only width a flag ever has; the enum's Bool member never appears here.
    internal const AssetValueType FlagValueType = AssetValueType.UInt8;

    // A state flag's name is matched by this shape rather than enumerated, which is what reaches
    // a flag a game update added under a name nothing here lists. tools/isagen holds the same
    // contract as the regular expression \bHas\w*State\b; keep the two in sync.
    internal const string FlagPrefix = "Has";
    internal const string FlagSuffix = "State";

    /// <summary>Whether a serialized field's name has the state-flag shape.</summary>
    internal static bool FlagShape(string name) =>
        name.StartsWith(FlagPrefix, StringComparison.Ordinal) && name.EndsWith(FlagSuffix, StringComparison.Ordinal);

    // Not a completeness floor: the build this was written against ships 1565 prefabs, so a
    // roster that lost a third of them still clears 1000 without comment. What this rules out is
    // an empty SourcePrefabs array being read as the real roster.
    internal const int MinPrefabs = 1000;

    // The state-flag layout Thing serializes, in serialized order. Does not include
    // HasContentsState: that name belongs to an animator hash on VendingMachine, not to a field
    // Thing serializes.
    internal static readonly ImmutableArray<string> ThingStateFields = ImmutableArray.Create(
        "HasErrorState",
        "HasPowerState",
        "HasActivateState",
        "HasLockState",
        "HasOnOffState",
        "HasModeState",
        "HasOpenState",
        "HasImportState",
        "HasImport2State",
        "HasExportState",
        "HasExport2State",
        "HasButton1State",
        "HasButton2State",
        "HasButton3State",
        "HasColorState",
        "HasAccessState");

    // Named rather than left out of ThingStateFields, so the layout check can tell a flag this
    // extraction decided against from a flag a game update introduced.
    private static readonly HashSet<string> UnmodelledStateFields = new(StringComparer.Ordinal)
    {
        "HasImport2State",
        "HasExport2State",
        "HasButton1State",
        "HasButton2State",
        "HasButton3State",
    };

    // Eight of these are every flag some CanLogicRead or CanLogicWrite body in the game reads.
    // HasImportState, HasExportState and HasAccessState are carried past that for reasons the
    // game source does not give; do not prune them to the eight, since being read outside a logic
    // surface body is not the rule either (HasExport2State is, and it is left out).
    internal static readonly ImmutableArray<string> StateFields =
        ThingStateFields.Where(name => !UnmodelledStateFields.Contains(name)).ToImmutableArray();

    /// <summary>
    /// Returns why the flags one prefab serializes are not the ThingStateFields layout, or null
    /// when they are. <paramref name="found"/> pairs each state-flag-shaped name with its width.
    /// </summary>
    internal static string? StateLayoutProblem(IReadOnlyList<(string Name, AssetValueType ValueType)> found)
    {
        List<string> repeated = found
            .GroupBy(field => field.Name, StringComparer.Ordinal)
            .Where(group => group.Count() > 1)
            .Select(group => group.Key)
            .ToList();

        var names = new HashSet<string>(found.Select(field => field.Name), StringComparer.Ordinal);
        List<string> added = names.Except(ThingStateFields, StringComparer.Ordinal).ToList();
        List<string> gone = ThingStateFields.Where(name => !names.Contains(name)).ToList();

        List<string> wrong = found
            .Where(field => field.ValueType != FlagValueType)
            .Select(field => $"{field.Name} as {field.ValueType}")
            .ToList();

        return JoinProblems(
            repeated.Count > 0 ? $"serializes more than one field named {JoinNames(repeated)}" : null,
            added.Count > 0 || gone.Count > 0
                ? $"serializes state flags this extraction does not model: added {JoinNames(added)}, missing {JoinNames(gone)}"
                : null,
            wrong.Count > 0
                ? $"serializes {JoinNames(wrong)} rather than as the {FlagValueType} a bool serializes to"
                : null);
    }

    /// <summary>
    /// Returns why <see cref="ThingClass"/> no longer declares the state flags as bools, or null
    /// when it does. A byte-backed enum also serializes as one byte, so it passes StateLayoutProblem too.
    /// </summary>
    internal static string? DeclaredFlagsProblem(IReadOnlyDictionary<string, DeclaredField> fields)
    {
        var missing = new List<string>();
        var wrong = new List<string>();
        var unwritten = new List<string>();
        foreach (string name in ThingStateFields)
        {
            if (!fields.TryGetValue(name, out DeclaredField field))
            {
                missing.Add(name);
            }
            else if (field.Type != FlagFieldType)
            {
                wrong.Add($"{name} as {field.Type}");
            }
            else if (!field.Serialized)
            {
                unwritten.Add(name);
            }
        }

        string? problem = JoinProblems(
            missing.Count > 0 ? $"declares no {JoinNames(missing)}" : null,
            wrong.Count > 0 ? $"declares {JoinNames(wrong)} rather than {FlagFieldType}" : null,
            unwritten.Count > 0 ? $"declares {JoinNames(unwritten)} where Unity does not serialize it" : null);
        return problem == null ? null : $"{ThingClass} {problem}";
    }

    /// <summary>
    /// Returns why <see cref="DeviceClass"/> no longer declares the power draw correctly, or null.
    /// Checked as serialized, not merely declared, or NonSerialized/shadowing would pass silently.
    /// </summary>
    internal static string? DeclaredPowerProblem(IReadOnlyDictionary<string, DeclaredField> fields)
    {
        if (!fields.TryGetValue(PowerField, out DeclaredField field))
        {
            return $"{DeviceClass} declares no {PowerField}, so every prefab would read as drawing nothing";
        }
        if (field.Type != PowerFieldType)
        {
            return $"{DeviceClass} declares {PowerField} as {field.Type} rather than {PowerFieldType}";
        }
        return field.Serialized
            ? null
            : $"{DeviceClass} declares {PowerField} where Unity does not serialize it, so every prefab would read as drawing nothing";
    }

    /// <summary>
    /// Returns why a roster the walk produced carries no power draw anywhere, or null when at least
    /// one prefab carries one — asked of what the walk read, unlike <see cref="DeclaredPowerProblem"/>.
    /// </summary>
    internal static string? PowerCoverageProblem(int declaring, int total) =>
        CoverageProblem(declaring, total, $"carries a {PowerField}", "drawing nothing");

    /// <summary>Same question as <see cref="PowerCoverageProblem"/>, asked of the slot list.</summary>
    internal static string? SlotCoverageProblem(int declaring, int total) =>
        CoverageProblem(declaring, total, "declares a slot", "holding nothing");

    // The shape both coverage questions share: a floor of one prefab on the whole roster, and a
    // sentence saying what the roster would otherwise be read as downstream.
    private static string? CoverageProblem(int declaring, int total, string carries, string reads) =>
        declaring > 0
            ? null
            : $"none of the {total} prefabs read {carries}, so the whole roster would read as {reads}";

    /// <summary>Names a prefab by the name it is keyed by downstream and the pathId it was read at.</summary>
    internal static string PrefabWhere(string name, long pathId) => $"the prefab {name} at pathId {pathId}";

    /// <summary>Names a prefab this reader has no name for yet, or could not read one for.</summary>
    internal static string PrefabWhere(long pathId) => $"the prefab at pathId {pathId}";

    // How many clauses a whole-roster refusal names before it stops naming them.
    private const int Sample = 5;

    // Repeats are dropped before sampling: a refusal decided over the whole roster is one many
    // prefabs reach the same way, so the same clause would otherwise crowd out the rest. The
    // counts around this call are taken before dedup, so scale is still said either way.
    private static string Sampled(IReadOnlyList<string> clauses)
    {
        List<string> distinct = clauses.Distinct(StringComparer.Ordinal).ToList();
        return string.Join(", ", distinct.Take(Sample)) +
            (distinct.Count > Sample ? $", and {distinct.Count - Sample} more" : string.Empty);
    }

    /// <summary>
    /// Returns why the walk refused some roster entries, or null when it refused none.
    /// <paramref name="total"/> is how many entries the roster carried in all.
    /// </summary>
    internal static string? RosterProblem(IReadOnlyList<string> refused, int total) =>
        refused.Count == 0
            ? null
            : $"the walk refused {refused.Count} of the {total} prefabs on the roster: {Sampled(refused)}";

    /// <summary>
    /// Returns why some power draws the walk read have no JSON spelling, or null when every one is
    /// finite. Checked ahead of the writer: Utf8JsonWriter would refuse NaN/infinity mid-file instead.
    /// </summary>
    internal static string? DrawsProblem(IReadOnlyList<(string Where, float Draw)> draws)
    {
        List<string> unwritable = draws
            .Where(draw => !float.IsFinite(draw.Draw))
            .Select(draw => $"{draw.Where} carries {draw.Draw}")
            .ToList();
        return unwritable.Count == 0
            ? null
            : $"the roster carries {unwritable.Count} of {draws.Count} {PowerField} values " +
                $"the artifact has no number for: {Sampled(unwritable)}";
    }

    /// <summary>
    /// Returns why the roster this reader would take is not forced, or null with
    /// <paramref name="chosen"/> the self-contained candidate — kept off sharedassets and scene files.
    /// </summary>
    internal static string? ChooseRoster(
        IReadOnlyList<(long PathId, int External, int Total)> candidates, string fileName, int seen, out int chosen)
    {
        chosen = 0;
        List<int> selfContained = Enumerable.Range(0, candidates.Count)
            .Where(i => candidates[i].External == 0)
            .ToList();

        if (selfContained.Count == 1)
        {
            int only = selfContained[0];
            (long pathId, _, int total) = candidates[only];
            if (total < MinPrefabs)
            {
                return $"the {WorldManagerClass} at pathId {pathId} carries {total} prefabs, want at least {MinPrefabs}";
            }
            chosen = only;
            return null;
        }

        if (seen == 0)
        {
            return $"{fileName} holds no {WorldManagerClass} at all, so the class the prefab roster hangs off was renamed or moved";
        }
        if (candidates.Count == 0)
        {
            return $"none of the {seen} {WorldManagerClass} in {fileName} carries {RosterField}, so the field the prefab roster is named by was renamed or moved";
        }
        string detail = selfContained.Count > 1
            ? string.Join("; ", selfContained.Select(i => "pathId " + candidates[i].PathId))
            : string.Join("; ", candidates.Select(c =>
                $"pathId {c.PathId}: {c.External} of {c.Total} entries point outside {fileName}"));
        return $"{fileName} holds {selfContained.Count} self-contained {WorldManagerClass} prefab rosters of the {candidates.Count} carrying {RosterField}, want exactly 1: {detail}";
    }

    /// <summary>
    /// A Unity build, ordered by the three numbers a class package declares its type ranges over.
    /// <paramref name="Text"/> breaks a tie the numbers cannot, e.g. between 2022.3.41f1 and .41b2.
    /// </summary>
    internal readonly record struct EngineVersion(int Major, int Minor, int Patch, string Text)
        : IComparable<EngineVersion>
    {
        public int CompareTo(EngineVersion other)
        {
            int ordered = (Major, Minor, Patch).CompareTo((other.Major, other.Minor, other.Patch));
            return ordered != 0 ? ordered : string.CompareOrdinal(Text, other.Text);
        }
    }

    /// <summary>
    /// Returns why the class package cannot describe the serialized files' engine, or null when it
    /// can. Major &lt;= 0 is the "not a real version" sentinel: no real Unity build has one that low.
    /// </summary>
    internal static string? ClassPackageEngineProblem(EngineVersion wanted, IReadOnlyList<EngineVersion> covered)
    {
        string? problem = JoinProblems(
            wanted.Major <= 0
                ? $"the serialized files name {wanted.Text}, which is not an engine version this reader can order a class package against"
                : null,
            covered.Count == 0
                ? $"the class package describes no engine version at all, so it cannot describe the engine the serialized files name, {wanted.Text}"
                : null);
        if (problem != null)
        {
            return problem;
        }

        EngineVersion newest = covered.Max();
        // Older than wanted: layouts that moved since are left out, not reported.
        return wanted.CompareTo(newest) <= 0
            ? null
            : $"the serialized files were written by {wanted.Text}, newer than the {newest.Text} the class package describes, so every engine type that moved since would be left out of the database rather than reported";
    }

    // What this reader is left without when the assembly carries no build stamp.
    internal const string NoBuild = "so the roster would name a build the logic surfaces cannot be held to";

    /// <summary>
    /// Returns why a present version resource still names no build, or null when it does. Refuses
    /// <c>default</c>: a stripped stamp reads 0.0.0.0, which would join against another stripped build.
    /// </summary>
    internal static string? AssemblyVersionProblem(FileVersion version, string path)
    {
        return version == default
            ? $"{path} is stamped {AssemblyVersion(version)} in its {VersionResource.Name}, {NoBuild}"
            : null;
    }

    /// <summary>The four-part file version as the roster spells it.</summary>
    internal static string AssemblyVersion(FileVersion version) =>
        $"{version.Major}.{version.Minor}.{version.Build}.{version.Revision}";

    /// <summary>
    /// The four-part version a PE's Win32 version resource carries. Travels as one value since
    /// nothing here can tell a version from the same four numbers handed over in another order.
    /// </summary>
    internal readonly record struct FileVersion(int Major, int Minor, int Build, int Revision);

    /// <summary>
    /// Byte layout of a serialized file's header, most-significant-byte-first. From
    /// <see cref="LargeVersion"/> the file size and data offset widen here from 32 to 64 bits.
    /// </summary>
    internal static class SerializedHeader
    {
        internal const int LargeVersion = 22;
        internal const int Small = 20;
        internal const int Large = 48;
        internal const int FileSizeAt = 4;
        internal const int VersionAt = 8;
        internal const int DataOffsetAt = 12;
        internal const int LargeFileSizeAt = 24;
        internal const int LargeDataOffsetAt = 32;
    }

    /// <summary>
    /// Byte layout of a class package's own header, least-significant-byte-first (unlike
    /// <see cref="SerializedHeader"/>'s). <see cref="HighestVersion"/> is the library's own ceiling.
    /// </summary>
    internal static class PackageHeader
    {
        internal const int Size = 20;
        internal const string Magic = "TPK*";
        internal const int HighestVersion = 1;
        internal const int VersionAt = 4;
        internal const int CompressedSizeAt = 12;
    }

    // The only two claims a serialized file's header makes that can be held against the file on
    // disk. Metadata size is counted from elsewhere depending on format version, and engine
    // version/byte order describe the file's content rather than its extent.
    internal readonly record struct SerializedExtent(long FileSize, long DataOffset);

    // Version kept a byte and size kept unsigned to hold out negatives the header cannot spell;
    // a negative version would otherwise pass the check below, which only refuses versions above
    // the highest this reader reads. The serialized file's size beside it is signed for the
    // opposite reason: its 64-bit header does let bytes that are not a length arrive below zero.
    internal readonly record struct PackageExtent(byte Version, uint CompressedSize);

    /// <summary>
    /// Returns why a file cannot be the serialized file the prefab roster is read from, or null.
    /// Asked before the asset library sees it, since that library does not reliably refuse a bad one.
    /// </summary>
    internal static string? SerializedFileProblem(
        SerializedExtent? extent, long length, string path, string consequence)
    {
        if (extent == null)
        {
            return $"{path}: {length} bytes, which is not even a serialized file header, {consequence}";
        }

        SerializedExtent claimed = extent.Value;
        string? problem = JoinProblems(
            claimed.FileSize <= 0 || claimed.FileSize > length
                ? $"declares itself {claimed.FileSize} bytes and is {length}"
                : null,
            claimed.DataOffset <= 0 || claimed.DataOffset > claimed.FileSize
                ? $"puts its asset data at offset {claimed.DataOffset} in the {claimed.FileSize} bytes it declares"
                : null);
        return problem == null ? null : $"{path}: this file {problem}, {consequence}";
    }

    /// <summary>
    /// Returns why a file cannot be the class package the engine layouts are built from, or null —
    /// catches a truncated package the asset library would otherwise silently read as zero-filled bytes.
    /// </summary>
    internal static string? ClassPackageFileProblem(
        PackageExtent? extent, long length, string path, string consequence)
    {
        if (extent == null)
        {
            return null;
        }

        PackageExtent claimed = extent.Value;

        // Widened explicitly: a 32-bit size plus a header constant would otherwise add in
        // whatever width the compiler picks, and the largest declarable body wraps to a small
        // positive there once the header is added, reading as a package claiming almost nothing.
        long declared = PackageHeader.Size + (long)claimed.CompressedSize;

        string? problem = JoinProblems(
            claimed.Version > PackageHeader.HighestVersion
                ? $"is written in class package format version {claimed.Version}, and this reader reads up to {PackageHeader.HighestVersion}"
                : null,
            claimed.CompressedSize == 0
                ? $"declares no class data at all behind its {PackageHeader.Size}-byte header"
                : null,
            declared > length
                ? $"declares {claimed.CompressedSize} bytes of class data behind its {PackageHeader.Size}-byte header and is {length}"
                : null);
        return problem == null ? null : $"{path}: this file {problem}, {consequence}";
    }

    /// <summary>
    /// Whether a failure from opening a build input means that input is wrong, not this reader
    /// meeting the unanticipated. Excludes OutOfMemoryException: that also means an undersized machine.
    /// </summary>
    internal static bool Unopenable(Exception failure) =>
        failure is IOException or NotSupportedException or UnauthorizedAccessException or BadImageFormatException;

    /// <summary>
    /// Every non-null problem joined into one sentence, or null when given none. Every argument is
    /// evaluated regardless of the others, so a change tripping several still names all of them.
    /// </summary>
    internal static string? JoinProblems(params string?[] problems)
    {
        string?[] found = problems.Where(problem => problem != null).ToArray();
        return found.Length > 0 ? string.Join("; ", found) : null;
    }

    /// <summary>
    /// A set of names in one clause, ordered so two refusals read side by side differ only where
    /// the sets do, and spelled "none" for an empty set so it reads as an answer rather than a gap.
    /// </summary>
    internal static string JoinNames(IEnumerable<string> names)
    {
        string joined = string.Join(", ", names.OrderBy(name => name, StringComparer.Ordinal));
        return joined.Length > 0 ? joined : "none";
    }
}
