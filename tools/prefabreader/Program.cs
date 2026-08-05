using System;
using System.Collections.Generic;
using System.Collections.Immutable;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Json;
using AssetsTools.NET;
using AssetsTools.NET.Extra;

namespace PrefabReader;

/// <summary>
/// Recovers the per-prefab facts the game keeps in its Unity serialized files -- existence,
/// hash, driving class, state flags, slots -- as JSON for tools/isagen to join against the C#.
/// </summary>
internal static class Program
{
    // Independent of the walk below: the class database must describe each of these on its own,
    // so a database built for the wrong engine can be missing one and not the other. Immutable
    // for the same reason Checks.ThingStateFields is: every reading below is a function of it.
    private static readonly ImmutableArray<AssetClassID> EngineClasses =
        ImmutableArray.Create(AssetClassID.MonoBehaviour, AssetClassID.MonoScript);

    private static int Main(string[] args) => Command(args, Console.Error, Run);

    /// <summary>
    /// Runs the walk and turns its outcome into an exit code. <paramref name="walk"/> and
    /// <paramref name="diagnostics"/> are parameters so a test can run this without a real depot.
    /// </summary>
    internal static int Command(string[] args, TextWriter diagnostics, Action<string, string, string> walk)
    {
        if (args.Length != 3)
        {
            diagnostics.WriteLine("usage: prefabreader <data-dir> <classdata.tpk> <out.json>");
            return 2;
        }

        try
        {
            walk(args[0], args[1], args[2]);
            return 0;
        }
        catch (RefusalException e)
        {
            diagnostics.WriteLine("prefabreader: " + e.Message);
            return 1;
        }
        catch (Exception e)
        {
            diagnostics.WriteLine($"prefabreader: {e}");
            return 1;
        }
    }

    private static void Run(string dataDir, string classPackage, string outPath)
    {
        // AssetsManager does not implement IDisposable despite holding real handles, so they are
        // given back by hand via UnloadAll, which also disposes the generator it was given.
        var manager = new AssetsManager();
        try
        {
            manager.UseTemplateFieldCache = true;
            manager.UseRefTypeManagerCache = true;
            manager.UseMonoTemplateFieldCache = true;

            string managed = Path.Combine(dataDir, Checks.ManagedDirectory);
            string? assemblyProblem = Declarations.InspectAssembly(
                Path.Combine(managed, Checks.AssemblyFile), out string? assemblyVersion);
            string? packageProblem = PackageProblem(
                classPackage,
                path => manager.LoadClassPackage(path),
                out ClassPackageFile? package);
            string? resourcesProblem = ResourcesProblem(
                Path.Combine(dataDir, Checks.ResourcesFile),
                path => manager.LoadAssetsFile(path, loadDeps: false),
                file => file.file.Metadata.Externals.Select(external => external.PathName).ToList(),
                out AssetsFileInstance? resources);
            string? unityVersion = resources?.file.Metadata.UnityVersion;

            // Each build input is asked about on its own before any is acted on, so a depot with
            // several inputs out of step reports all of them at once. The engine question below
            // runs only once both package and resources opened -- there is nothing to order until then.
            string? problem = Checks.JoinProblems(
                assemblyProblem,
                packageProblem,
                resourcesProblem,
                package != null && resources != null
                    ? ClassDatabaseProblem(
                        unityVersion,
                        package.TpkTypeTree.Versions,
                        version => UnresolvedEngineClasses(manager.LoadClassDatabaseFromPackage, version))
                    : null);
            if (problem != null)
            {
                throw new RefusalException(problem);
            }

            // problem == null here implies all four of the above are populated; the null-forgiving
            // operators below rely on that invariant, which the compiler cannot see through JoinProblems.
            manager.MonoTempGenerator = new MonoCecilTempGenerator(managed);
            List<Prefab> prefabs = ReadRoster(manager, resources!);

            Write(outPath, assemblyVersion!, prefabs);
            foreach (string line in Report(unityVersion!, assemblyVersion!, prefabs))
            {
                Console.WriteLine(line);
            }
        }
        finally
        {
            Unload(manager);
        }
    }

    /// <summary>
    /// Returns why a build input could not be opened, or null with <paramref name="opened"/> set
    /// to what opening it produced. <paramref name="consequence"/> says what is lost without it.
    /// </summary>
    internal static string? InputProblem<T>(
        string path, string what, string consequence, Func<string, T> open, out T? opened)
    {
        opened = default;
        if (!File.Exists(path))
        {
            return $"{path}: no {what} here, {consequence}";
        }

        try
        {
            opened = open(path);
        }
        catch (Exception e) when (Checks.Unopenable(e))
        {
            return $"{path}: this {what} does not read, {consequence}: {e.Message}";
        }
        if (opened is null)
        {
            throw new InvalidOperationException(
                $"{path}: opening this {what} answered nothing and raised nothing, which is neither thing it does");
        }
        return null;
    }

    // What each refusal ends with: NoRoster when the roster's own file cannot be read, NotAllThere
    // for a file it declares that also cannot, NoEngineTypes when the class package cannot.
    private const string NoRoster = "so there is no prefab roster to read";
    private const string NotAllThere = "so the roster would be read through a file that is not all there";
    private const string NoEngineTypes =
        "so nothing here describes the engine types the serialized files were written by";

    // Returns what a build input's own header says is wrong with it, or null when the header
    // faults nothing or the file cannot even be opened/read -- that failure is InputProblem's.
    private static string? Claimed<T>(string path, Func<Stream, T> read, Func<T, long, string?> decide)
    {
        try
        {
            using FileStream file = File.OpenRead(path);
            return decide(read(file), file.Length);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    /// <summary>
    /// Returns why the class package cannot be read, or null with <paramref name="opened"/> set
    /// to what opening it produced. Checked against its header first, since the library accepts a truncated one silently.
    /// </summary>
    internal static string? PackageProblem(
        string path, Func<string, ClassPackageFile> open, out ClassPackageFile? opened)
    {
        opened = null;
        string? problem = Claimed(
            path, PackageExtent, (extent, length) => Checks.ClassPackageFileProblem(extent, length, path, NoEngineTypes));
        return problem ?? InputProblem(path, "class package", NoEngineTypes, open, out opened);
    }

    /// <summary>
    /// Returns why the serialized files the roster is read from and through cannot be read, or
    /// null with <paramref name="opened"/> set to the one the roster itself is taken from.
    /// </summary>
    internal static string? ResourcesProblem(
        string path,
        Func<string, AssetsFileInstance> open,
        Func<AssetsFileInstance, IReadOnlyList<string>> externals,
        out AssetsFileInstance? opened)
    {
        opened = null;
        string? problem = Claimed(
            path, SerializedExtent, (extent, length) => Checks.SerializedFileProblem(extent, length, path, NoRoster));
        if (problem != null)
        {
            return problem;
        }

        problem = InputProblem(path, "serialized file", NoRoster, open, out opened);
        if (opened != null)
        {
            problem = DeclaredProblems(opened, open, externals);
        }
        if (problem != null)
        {
            opened = null;
        }
        return problem;
    }

    // Opens every file the roster's file declares, transitively, whether the walk uses them or
    // not -- the alternative is the asset library opening them anyway, unlooked-at. A declared
    // file absent from the depot is walked past, matching both the library's own answer and the
    // fetch's contract; every problem among the rest is collected, not just the first.
    private static string? DeclaredProblems(
        AssetsFileInstance file,
        Func<string, AssetsFileInstance> open,
        Func<AssetsFileInstance, IReadOnlyList<string>> externals)
    {
        var held = new HashSet<string>(StringComparer.Ordinal) { LookupKey(file.path) };
        var pending = new Queue<AssetsFileInstance>();
        pending.Enqueue(file);

        var problems = new List<string>();
        while (pending.Count > 0)
        {
            AssetsFileInstance declaring = pending.Dequeue();
            foreach (string declared in externals(declaring))
            {
                string? found = Beside(declaring.path, declared);
                if (found == null || !held.Add(LookupKey(found)))
                {
                    continue;
                }

                string? problem = Claimed(
                    found, SerializedExtent, (extent, length) => Checks.SerializedFileProblem(extent, length, found, NotAllThere));
                if (problem == null)
                {
                    problem = InputProblem(found, "serialized file", NotAllThere, open, out AssetsFileInstance? reached);
                    if (problem == null)
                    {
                        // InputProblem answers a file exactly where it faults
                        // none, raising rather than answering nothing twice.
                        pending.Enqueue(reached!);
                        continue;
                    }
                }
                problems.Add(problem);
            }
        }
        return Checks.JoinProblems(problems.ToArray());
    }

    // Mirrors the asset library's own external-file search (beside the declaring file, as
    // declared then by bare name), so this reader never accepts or rejects a pointer the library
    // would resolve differently. Three of the library's other candidates are left out: bundle-only
    // fallback, since this reader opens no bundles, and two that reduce to what is checked here.
    private static string? Beside(string path, string declared)
    {
        // GetDirectoryName answers null only for a root path, which the path a
        // serialized file was opened under is not.
        string directory = Path.GetDirectoryName(path)!;
        string asDeclared = Path.Combine(directory, declared);
        if (File.Exists(asDeclared))
        {
            return asDeclared;
        }
        string byName = Path.Combine(directory, Path.GetFileName(declared));
        return File.Exists(byName) ? byName : null;
    }

    // The name the asset library holds an opened serialized file under (its own rule), which is
    // why a file opened here is one the library will not open a second time.
    private static string LookupKey(string path) => Path.GetFileName(path).ToLowerInvariant();

    /// <summary>
    /// Returns what the front of a serialized file claims about its own size (most-significant-
    /// byte-first), or null when too short to claim it. Format version picks the 32- or 64-bit pair.
    /// </summary>
    internal static Checks.SerializedExtent? SerializedExtent(Stream file)
    {
        var head = new byte[Checks.SerializedHeader.Large];
        int read = Fill(file, head);
        if (read < Checks.SerializedHeader.Small)
        {
            return null;
        }
        if (Be32(head, Checks.SerializedHeader.VersionAt) < Checks.SerializedHeader.LargeVersion)
        {
            return new Checks.SerializedExtent(
                Be32(head, Checks.SerializedHeader.FileSizeAt), Be32(head, Checks.SerializedHeader.DataOffsetAt));
        }
        return read < Checks.SerializedHeader.Large
            ? null
            : new Checks.SerializedExtent(
                Be64(head, Checks.SerializedHeader.LargeFileSizeAt), Be64(head, Checks.SerializedHeader.LargeDataOffsetAt));
    }

    /// <summary>
    /// Returns what the front of a class package claims about its own extent (least-significant-
    /// byte-first, unlike the serialized file header), or null for a file without that format's magic.
    /// </summary>
    internal static Checks.PackageExtent? PackageExtent(Stream file)
    {
        var head = new byte[Checks.PackageHeader.Size];
        if (Fill(file, head) < head.Length ||
            Encoding.UTF8.GetString(head, 0, Checks.PackageHeader.Magic.Length) != Checks.PackageHeader.Magic)
        {
            return null;
        }
        return new Checks.PackageExtent(
            head[Checks.PackageHeader.VersionAt], Le32(head, Checks.PackageHeader.CompressedSizeAt));
    }

    // Reads until the buffer is full or the stream is out, since a single Read call may hand back
    // fewer bytes than asked and a naive single-shot read would leave stale zeros as data.
    private static int Fill(Stream file, byte[] head)
    {
        int read = 0;
        while (read < head.Length)
        {
            int got = file.Read(head, read, head.Length - read);
            if (got == 0)
            {
                break;
            }
            read += got;
        }
        return read;
    }

    private static long Be32(byte[] bytes, int at) =>
        ((long)bytes[at] << 24) | ((long)bytes[at + 1] << 16) | ((long)bytes[at + 2] << 8) | bytes[at + 3];

    private static long Be64(byte[] bytes, int at)
    {
        long value = 0;
        for (int i = 0; i < 8; i++)
        {
            value = (value << 8) | bytes[at + i];
        }
        return value;
    }

    // Unsigned unlike Be32/Be64 above: the class package's own claim fits a uint outright, while
    // the serialized file's pair is read into a wider signed type so a claim that is not a real
    // length still arrives as a number instead of wrapping.
    private static uint Le32(byte[] bytes, int at) =>
        ((uint)bytes[at + 3] << 24) | ((uint)bytes[at + 2] << 16) | ((uint)bytes[at + 1] << 8) | bytes[at];

    // Reports a teardown failure rather than raising it: raising here would replace a refusal
    // already decided (the sentence a build-log reader must act on) with this instead, and a
    // roster already written is readable whether or not the handles came back cleanly.
    private static void Unload(AssetsManager manager)
    {
        try
        {
            manager.UnloadAll(unloadClassData: true);
        }
        catch (Exception e)
        {
            Console.Error.WriteLine($"prefabreader: giving the serialized files back failed: {e}");
        }
    }

    /// <summary>
    /// Settles the engine type layouts on the serialized files' version, returning why they could
    /// not be settled, or null. Parameters stand in for a pinned class package this repo does not carry.
    /// </summary>
    internal static string? ClassDatabaseProblem(
        string? unityVersion,
        IReadOnlyList<UnityVersion> covered,
        Func<string, IReadOnlyList<string>> build)
    {
        string? problem = Checks.ClassPackageEngineProblem(
            Engine(unityVersion),
            covered.Select(Engine).ToList());
        if (problem != null)
        {
            return problem;
        }

        // A header naming no engine at all is one of the shapes that check
        // refuses, so what is left here is a version the files spelled.
        IReadOnlyList<string> unresolved = build(unityVersion!);
        return unresolved.Count > 0
            ? $"the class database built for Unity {unityVersion} describes no {Checks.JoinNames(unresolved)}, so the assets the roster is read out of have no layout"
            : null;
    }

    /// <summary>
    /// Builds the class database for a version and returns which of EngineClasses it describes no
    /// layout for. <paramref name="build"/> stands in for the real, pinned-package call for testing.
    /// </summary>
    internal static IReadOnlyList<string> UnresolvedEngineClasses(
        Func<string, ClassDatabaseFile> build, string unityVersion)
    {
        ClassDatabaseFile database = build(unityVersion);
        return Unresolved(id => database.FindAssetClassByID((int)id) != null);
    }

    /// <summary>Names the EngineClasses <paramref name="described"/> answers false for.</summary>
    internal static IReadOnlyList<string> Unresolved(Func<AssetClassID, bool> described) =>
        EngineClasses.Where(id => !described(id)).Select(id => id.ToString()).ToList();

    private static Checks.EngineVersion Engine(UnityVersion version) =>
        new(version.major, version.minor, version.patch, "Unity " + version);

    // A header the reader left null, one with unparseable text, or a number too wide for the int
    // UnityVersion keeps each part in, all become the zero-major "not a version" sentinel Checks
    // orders behind everything real; FormatException/OverflowException are exactly what the
    // UnityVersion constructor throws for the last two.
    private static Checks.EngineVersion Engine(string? named)
    {
        if (named == null)
        {
            return new Checks.EngineVersion(0, 0, 0, "no engine at all");
        }

        try
        {
            return Engine(new UnityVersion(named));
        }
        catch (Exception e) when (e is FormatException or OverflowException)
        {
            return new Checks.EngineVersion(0, 0, 0, $"the text \"{named}\"");
        }
    }

    private static List<Prefab> ReadRoster(AssetsManager manager, AssetsFileInstance resources)
    {
        (long rosterPathId, AssetTypeValueField sourcePrefabs) = FindSourcePrefabs(manager, resources);
        string rosterWhere = $"the prefab roster at pathId {rosterPathId}";
        return Roster(
            sourcePrefabs.Children.Count,
            index => ReadPrefab(
                sourcePrefabs.Children[index],
                $"entry {index} of {rosterWhere}",
                (fileId, pathId) => manager.GetExtAsset(resources, fileId, pathId),
                asset => TryScriptClass(manager, asset.file, asset.info)));
    }

    /// <summary>
    /// Returns every prefab of a roster of <paramref name="entries"/> entries; a refused entry is
    /// collected, not raised, so <see cref="Checks.RosterProblem"/> can report the whole roster at once.
    /// </summary>
    internal static List<Prefab> Roster(int entries, Func<int, Prefab> read)
    {
        var prefabs = new List<Prefab>(entries);
        var refused = new List<string>();
        for (int index = 0; index < entries; index++)
        {
            try
            {
                prefabs.Add(read(index));
            }
            catch (RefusalException e)
            {
                refused.Add(e.Message);
            }
        }

        string? problem = Checks.RosterProblem(refused, entries);
        if (problem != null)
        {
            throw new RefusalException(problem);
        }
        return prefabs;
    }

    // Returns the prefab roster off the one WorldManager SelectRoster settles on.
    private static (long PathId, AssetTypeValueField Array) FindSourcePrefabs(AssetsManager manager, AssetsFileInstance file) =>
        SourcePrefabs(
            file.file.GetAssetsOfType(AssetClassID.MonoBehaviour),
            info => TryScriptClass(manager, file, info),
            info => manager.GetBaseField(file, info),
            file.name);

    /// <summary>
    /// Returns the roster SelectRoster settles on. A WorldManager <paramref name="read"/> answers
    /// nothing for stops the walk rather than being skipped: that is a seam failure, not a refusal.
    /// </summary>
    internal static (long PathId, AssetTypeValueField Array) SourcePrefabs(
        IEnumerable<AssetFileInfo> assets,
        Func<AssetFileInfo, string?> scriptClass,
        Func<AssetFileInfo, AssetTypeValueField?> read,
        string fileName)
    {
        var candidates = new List<(long PathId, int External, int Total)>();
        var arrays = new List<AssetTypeValueField>();
        int seen = 0;
        foreach (AssetFileInfo info in assets)
        {
            if (scriptClass(info) != Checks.WorldManagerClass)
            {
                continue;
            }
            seen++; // Distinguishes a renamed WorldManager class (seen == 0) from a renamed roster field.
            string where = $"the {Checks.WorldManagerClass} at pathId {info.PathId}";
            AssetTypeValueField worldManager = read(info)
                ?? throw new InvalidOperationException(
                    $"deserializing {where} answered nothing and raised nothing, which is neither thing it does");
            if (Candidate(worldManager, where) is not (AssetTypeValueField entries, int external, int total))
            {
                continue;
            }
            candidates.Add((info.PathId, external, total));
            arrays.Add(entries);
        }
        return SelectRoster(candidates, arrays, fileName, seen);
    }

    /// <summary>
    /// Returns the roster one WorldManager carries with what the choice between rosters is made on,
    /// or null when it carries none — legitimate, since only some assets of the class carry the field.
    /// </summary>
    internal static (AssetTypeValueField Entries, int External, int Total)? Candidate(
        AssetTypeValueField worldManager, string where)
    {
        AssetTypeValueField roster = One(worldManager, Checks.RosterField, where);
        if (roster.IsDummy)
        {
            return null;
        }
        AssetTypeValueField entries = Require(roster, Checks.ArrayField, where);
        return (entries, ExternalEntries(entries, where), entries.Children.Count);
    }

    /// <summary>
    /// Returns how many of a roster's prefab pointers name a file other than the one it was read
    /// from: a pointer's file id is 0 exactly when it names its own file.
    /// </summary>
    internal static int ExternalEntries(AssetTypeValueField entries, string where) =>
        entries.Children
            .Select((pptr, index) => RequireInt(pptr, Checks.FileIdField, $"entry {index} of {where}"))
            .Count(fileId => fileId != 0);

    /// <summary>The roster <see cref="Checks.ChooseRoster"/> settles on, paired with its pathId.</summary>
    internal static (long PathId, AssetTypeValueField Array) SelectRoster(
        IReadOnlyList<(long PathId, int External, int Total)> candidates,
        IReadOnlyList<AssetTypeValueField> arrays,
        string fileName,
        int seen)
    {
        string? problem = Checks.ChooseRoster(candidates, fileName, seen, out int chosen);
        if (problem != null)
        {
            throw new RefusalException(problem);
        }
        return (candidates[chosen].PathId, arrays[chosen]);
    }

    /// <summary>
    /// Returns one roster entry, following the pointer and naming its class off the pointed-to
    /// asset's own file — an entry may point into a different serialized file than the roster's.
    /// </summary>
    internal static Prefab ReadPrefab(
        AssetTypeValueField pptr,
        string entryWhere,
        Func<int, long, AssetExternal> read,
        Func<AssetExternal, string?> scriptClass)
    {
        int fileId = RequireInt(pptr, Checks.FileIdField, entryWhere);
        long pathId = RequireLong(pptr, Checks.PathIdField, entryWhere);
        AssetExternal asset = read(fileId, pathId);
        if (asset.baseField == null)
        {
            throw new RefusalException($"prefab {fileId}:{pathId} does not deserialize");
        }
        return BuildPrefab(asset.baseField, scriptClass(asset), pathId);
    }

    /// <summary>
    /// Returns everything this reader takes off one deserialized prefab. Every value is read at
    /// the exact width the game declares: the library's conversions do not refuse a mismatched width.
    /// </summary>
    internal static Prefab BuildPrefab(AssetTypeValueField asset, string? script, long pathId)
    {
        // Read before anything is layout-checked, so a layout failure can still name the prefab.
        // An empty name is a distinct case from an absent field: the field is present but empty,
        // and since name is the downstream lookup key, an empty one is unusable regardless.
        string nameWhere = Checks.PrefabWhere(pathId);
        string name = RequireString(asset, Checks.PrefabNameField, nameWhere);
        if (name.Length == 0)
        {
            throw new RefusalException(
                $"{nameWhere} carries an empty {Checks.PrefabNameField}, so nothing downstream could name it");
        }
        string where = Checks.PrefabWhere(name, pathId);

        // The no-script sentinel (walked past when searching for the roster) is not a valid state
        // for a prefab already on the roster: it means the join downstream cannot find the class.
        // Asked alongside the layout check, not before it, since neither depends on the other.
        string? problem = Checks.JoinProblems(
            script == null ? "names no script type" : null,
            Checks.StateLayoutProblem(StateFlags(asset)));
        if (problem != null)
        {
            throw new RefusalException($"{where} {problem}");
        }

        var state = new Dictionary<string, bool>(Checks.StateFields.Length, StringComparer.Ordinal);
        foreach (string field in Checks.StateFields)
        {
            state[field] = RequireFlag(asset, field, where);
        }

        // An absent draw means the prefab is not a device: the declaration check in Declarations
        // and the roster-wide coverage check are what make that the only thing it can mean.
        AssetTypeValueField usedPower = One(asset, Checks.PowerField, where);
        AssetTypeValueField slots = Require(Require(asset, Checks.SlotsField, where), Checks.ArrayField, where);

        return new Prefab(
            name,
            pathId,
            RequireInt(asset, Checks.PrefabHashField, where),
            // The clause above refuses the prefab that names no script type.
            script!,
            state,
            usedPower.IsDummy ? null : Widths(usedPower, Checks.PowerField, where, AssetValueType.Float).AsFloat,
            slots.Children.Select((slot, index) => ReadSlot(slot, $"slot {index} of {where}")).ToList());
    }

    // Every field of a prefab whose name has Checks.FlagShape, in serialized order, paired with
    // the width it arrived at.
    private static List<(string Name, AssetValueType ValueType)> StateFlags(AssetTypeValueField prefab) =>
        prefab.Children
            .Where(field => Checks.FlagShape(field.FieldName))
            .Select(field => (field.FieldName, field.TemplateField.ValueType))
            .ToList();

    // The width is checked explicitly first: AsUShort on a wider field either silently keeps a
    // value that happens to fit or throws OverflowException with no mention of the slot or field.
    private static Slot ReadSlot(AssetTypeValueField slot, string where)
    {
        return new Slot(Require(slot, Checks.SlotClassField, where, AssetValueType.UInt16).AsUShort);
    }

    private static AssetTypeValueField Require(AssetTypeValueField owner, string field, string where)
    {
        AssetTypeValueField value = One(owner, field, where);
        if (value.IsDummy)
        {
            throw new RefusalException($"{where} has no {field} field");
        }
        return value;
    }

    private static AssetTypeValueField Require(AssetTypeValueField owner, string field, string where, AssetValueType want) =>
        Widths(Require(owner, field, where), field, where, want);

    // The field of that name, refusing an owner that serializes more than one: a subclass
    // shadowing a base field's name leaves both in the file, the library's indexer answers the
    // first, and nothing here says which one is meant. StateLayoutProblem matches state flags by
    // shape instead, for the same reason.
    private static AssetTypeValueField One(AssetTypeValueField owner, string field, string where)
    {
        int found = owner.Children.Count(child => child.FieldName == field);
        if (found > 1)
        {
            throw new RefusalException(
                $"{where} serializes {found} fields named {field}, and this reader would read only the first");
        }
        return owner[field];
    }

    // Refuses a field that did not arrive as the width the game declares it over.
    private static AssetTypeValueField Widths(AssetTypeValueField value, string field, string where, AssetValueType want)
    {
        AssetValueType got = value.TemplateField.ValueType;
        if (got != want)
        {
            throw new RefusalException($"{where} serializes {field} as {got} rather than as {want}");
        }
        return value;
    }

    private static string RequireString(AssetTypeValueField owner, string field, string where) =>
        Require(owner, field, where, AssetValueType.String).AsString;

    private static int RequireInt(AssetTypeValueField owner, string field, string where) =>
        Require(owner, field, where, AssetValueType.Int32).AsInt;

    private static long RequireLong(AssetTypeValueField owner, string field, string where) =>
        Require(owner, field, where, AssetValueType.Int64).AsLong;

    // Unity writes a bool as a byte whose only values are 0 and 1; a byte outside that pair means
    // this reader is misreading the field as something it is not.
    private static bool RequireFlag(AssetTypeValueField owner, string field, string where)
    {
        byte value = Require(owner, field, where, Checks.FlagValueType).AsByte;
        if (value > 1)
        {
            throw new RefusalException($"{where} serializes {field} as the byte {value} rather than as a bool");
        }
        return value == 1;
    }

    // Returns the namespace-qualified class name, or null for an asset none can be named for
    // (no-script sentinel, or MonoScript naming no class) — both walked past when searching for
    // the roster, so a roster prefab hitting either is BuildPrefab's refusal to make.
    private static string? TryScriptClass(AssetsManager manager, AssetsFileInstance file, AssetFileInfo info) =>
        ScriptClass(
            file.file.Metadata.ScriptTypes,
            info.GetScriptIndex(file.file),
            file.name,
            pptr => manager.GetExtAsset(file, pptr.FileId, pptr.PathId).baseField);

    /// <summary>
    /// Returns the qualified class name a script table entry stands for, or null when the index
    /// names no entry. <paramref name="read"/> stands in for the real deserialize call, for testing.
    /// </summary>
    internal static string? ScriptClass(
        IReadOnlyList<AssetPPtr> scripts, ushort index, string fileName, Func<AssetPPtr, AssetTypeValueField?> read)
    {
        AssetPPtr? pptr = ScriptPointer(scripts, index, fileName);
        if (pptr == null)
        {
            return null;
        }
        AssetTypeValueField? script = read(pptr);
        if (script == null)
        {
            throw new RefusalException($"script {pptr.FileId}:{pptr.PathId} does not deserialize");
        }
        return ScriptClassName(script, $"the script at {pptr.FileId}:{pptr.PathId}");
    }

    /// <summary>
    /// Returns the script table entry an asset's script index names, or null for the no-script
    /// sentinel. An empty table is refused, not tolerated -- the reader always builds this list.
    /// </summary>
    internal static AssetPPtr? ScriptPointer(IReadOnlyList<AssetPPtr> scripts, ushort index, string fileName)
    {
        if (scripts.Count == 0)
        {
            throw new RefusalException(
                $"{fileName} carries no script type table, so nothing in it names the class driving it");
        }
        return index >= scripts.Count ? null : scripts[index];
    }

    /// <summary>
    /// Returns the qualified name a deserialized MonoScript stands for, or null for one naming no
    /// class, matching the no-script sentinel so the roster search walks past both alike.
    /// </summary>
    internal static string? ScriptClassName(AssetTypeValueField script, string where)
    {
        string ns = RequireString(script, Checks.NamespaceField, where);
        string name = RequireString(script, Checks.ClassNameField, where);
        if (name.Length == 0)
        {
            return null;
        }
        return ns.Length > 0 ? ns + "." + name : name;
    }

    /// <summary>
    /// Emits the roster tools/isagen reads. used_power and slots are absent exactly when a prefab
    /// has none; state always writes every field, so none can read undecided downstream from omission.
    /// </summary>
    internal static void Write(string path, string assemblyVersion, IReadOnlyList<Prefab> prefabs)
    {
        // Checked together, and before anything is written: a game update moving one of these
        // fields is the kind that moves another, and a draw JSON can't spell means a coverage bug.
        string? problem = Checks.JoinProblems(
            Checks.PowerCoverageProblem(prefabs.Count(prefab => prefab.UsedPower.HasValue), prefabs.Count),
            Checks.SlotCoverageProblem(prefabs.Count(prefab => prefab.Slots.Count > 0), prefabs.Count),
            Checks.DrawsProblem(prefabs
                .Where(prefab => prefab.UsedPower.HasValue)
                .Select(prefab => (Where: Checks.PrefabWhere(prefab.Name, prefab.PathId), Draw: prefab.UsedPower!.Value))
                .ToList()));
        if (problem != null)
        {
            throw new RefusalException(problem);
        }

        // Named rather than left to the writer's default (the host machine's), so the artifact's
        // bytes do not depend on which machine produced it.
        var format = new JsonWriterOptions { Indented = true, NewLine = "\n" };

        string staging = path + Staging;
        using (FileStream stream = File.Create(staging))
        using (var w = new Utf8JsonWriter(stream, format))
        {
            w.WriteStartObject();
            w.WriteString("assembly_version", assemblyVersion);
            w.WriteStartArray("prefabs");
            foreach (Prefab prefab in prefabs)
            {
                w.WriteStartObject();
                w.WriteString("name", prefab.Name);
                w.WriteNumber("hash", prefab.Hash);
                w.WriteString("script", prefab.Script);
                w.WriteStartObject("state");
                foreach (string field in Checks.StateFields)
                {
                    w.WriteBoolean(field, prefab.State[field]);
                }
                w.WriteEndObject();
                if (prefab.UsedPower.HasValue)
                {
                    w.WriteNumber("used_power", prefab.UsedPower.Value);
                }
                if (prefab.Slots.Count > 0)
                {
                    w.WriteStartArray("slots");
                    foreach (Slot slot in prefab.Slots)
                    {
                        w.WriteStartObject();
                        w.WriteNumber("class", slot.Class);
                        w.WriteEndObject();
                    }
                    w.WriteEndArray();
                }
                w.WriteEndObject();
            }
            w.WriteEndArray();
            w.WriteEndObject();
        }
        // Built under another name and renamed into place atomically, so a failure partway
        // through writing never leaves a reader downstream seeing a half-written artifact.
        File.Move(staging, path, overwrite: true);
    }

    // What a failed write leaves behind, under a name nothing reads; the next run overwrites it.
    internal const string Staging = ".partial";

    // States what the walk saw, so a game update that moves the roster shows up as a changed
    // count in the build log rather than only as a changed artifact.
    internal static string[] Report(string unityVersion, string assemblyVersion, IReadOnlyList<Prefab> prefabs) =>
        new[]
        {
            "unity version: " + unityVersion,
            "assembly version: " + assemblyVersion,
            "prefabs: " + prefabs.Count,
            "distinct script classes: " + prefabs.Select(p => p.Script).Distinct().Count(),
            "prefabs declaring a power draw: " + prefabs.Count(p => p.UsedPower.HasValue),
            "prefabs with slots: " + prefabs.Count(p => p.Slots.Count > 0),
            "slots: " + prefabs.Sum(p => p.Slots.Count),
        };

    /// <summary>
    /// One roster entry. <paramref name="PathId"/> reaches no artifact key; it is carried so a
    /// refusal decided over the whole roster can name a prefab the way the walk that read it did.
    /// </summary>
    internal sealed record Prefab(
        string Name,
        long PathId,
        int Hash,
        string Script,
        IReadOnlyDictionary<string, bool> State,
        float? UsedPower,
        IReadOnlyList<Slot> Slots);

    /// <summary>One slot a prefab declares. <paramref name="Class"/> is the Slot.Class ordinal, serialized as Type.</summary>
    internal sealed record Slot(ushort Class);
}
