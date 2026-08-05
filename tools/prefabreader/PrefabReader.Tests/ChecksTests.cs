using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using AssetsTools.NET;
using AssetsTools.NET.Extra;
using Xunit;

namespace PrefabReader.Tests;

public class ChecksTests
{
    // Transcribed from the game source in declaration order, not derived from
    // the table under test: a derived expectation holds for any table at all.
    // The decompiled source is not checked in, so this list is what a reader of
    // a game update compares against, and moving a name is meant to be a diff.
    private static readonly string[] ThingFlags =
    {
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
        "HasAccessState",
    };

    // Spelled out rather than derived, for the reason the layout above is. Eight
    // are every flag some CanLogicRead or CanLogicWrite body in the game reads;
    // HasImportState, HasExportState and HasAccessState are carried past that
    // criterion and the game source does not say why.
    private static readonly string[] ModelledFlagNames =
    {
        "HasErrorState",
        "HasPowerState",
        "HasActivateState",
        "HasLockState",
        "HasOnOffState",
        "HasModeState",
        "HasOpenState",
        "HasImportState",
        "HasExportState",
        "HasColorState",
        "HasAccessState",
    };

    [Fact]
    public void ThingStateFieldsAreTheFlagsTheGameSerializes()
    {
        Assert.Equal(ThingFlags, Checks.ThingStateFields);
    }

    // Every other test builds its fixture out of the constant, so a constant
    // moved to a name the game does not declare takes those fixtures with it and
    // they go on agreeing. Only a literal disagrees.
    [Fact]
    public void TheGameFactsAreSpelledOut()
    {
        (string Name, string Got, string Want)[] cases =
        {
            ("the class declaring the state flags", Checks.ThingClass, "Assets.Scripts.Objects.Thing"),
            ("the class declaring the power draw", Checks.DeviceClass, "Assets.Scripts.Objects.Pipes.Device"),
            ("the field the power draw is read from", Checks.PowerField, "UsedPower"),
            ("the class the prefab roster hangs off", Checks.WorldManagerClass, "WorldManager"),
            ("the game assembly beside the serialized files", Checks.AssemblyFile, "Assembly-CSharp.dll"),
            ("the field the prefab name is read from", Checks.PrefabNameField, "PrefabName"),
            ("the field the prefab hash is read from", Checks.PrefabHashField, "PrefabHash"),
            ("the field a prefab's slots are read from", Checks.SlotsField, "Slots"),
            ("the field the prefab roster is read from", Checks.RosterField, "SourcePrefabs"),
            ("the field a slot's class ordinal is read from", Checks.SlotClassField, "Type"),
            ("the child a serialized array hangs its entries off", Checks.ArrayField, "Array"),
            ("the file half of a pointer to another asset", Checks.FileIdField, "m_FileID"),
            ("the path half of a pointer to another asset", Checks.PathIdField, "m_PathID"),
            ("the namespace half of the class a MonoScript stands for", Checks.NamespaceField, "m_Namespace"),
            ("the bare half of the class a MonoScript stands for", Checks.ClassNameField, "m_ClassName"),
            ("the directory the game keeps its assembly in", Checks.ManagedDirectory, "Managed"),
            ("the serialized file the prefab roster is read out of", Checks.ResourcesFile, "resources.assets"),
            ("what a serialized bool arrives as", Checks.FlagValueType.ToString(), "UInt8"),
            ("the prefix of a state flag's name", Checks.FlagPrefix, "Has"),
            ("the suffix of a state flag's name", Checks.FlagSuffix, "State"),
        };

        var failures = new List<string>();
        foreach ((string name, string got, string want) in cases)
        {
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void StateFieldsAreTheModelledFlagsInSerializedOrder()
    {
        Assert.Equal(ModelledFlagNames, Checks.StateFields);
    }

    // This shape is the contract tools/isagen spells as the regular expression
    // \bHas\w*State\b, which matches the bare concatenation too -- so both sides
    // take HasState for a flag. The two neighbours Thing serializes either side
    // of the flag block are what hold each half of the shape to deciding.
    [Fact]
    public void FlagShapeTakesAFlagByBothHalvesOfItsName()
    {
        (string Name, string Field, bool Want)[] cases =
        {
            ("a flag the game serializes", "HasPowerState", true),
            ("a flag under a name this reader does not model", "HasWidgetState", true),
            ("the bare concatenation", "HasState", true),
            ("the prefix without the suffix", "HasRunOnAtmospherics", false),
            ("the suffix without the prefix", "DamageState", false),
            ("the suffix inside a longer name", "HasPowerStateChanged", false),
            ("the prefix inside a longer name", "ThingHasPowerState", false),
            ("neither half", "PrefabName", false),
            ("the prefix alone", "Has", false),
            ("the suffix alone", "State", false),
        };

        var failures = new List<string>();
        foreach ((string name, string field, bool want) in cases)
        {
            bool got = Checks.FlagShape(field);
            if (got != want)
            {
                failures.Add($"{name}: {field} read as {(got ? "a flag" : "not a flag")}, want {(want ? "a flag" : "not a flag")}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A game update that adds a flag is the kind that widens another, so the
    // last two rows hold the three answers to being asked together.
    [Fact]
    public void StateLayoutProblemHoldsAPrefabToTheModelledLayout()
    {
        const string unmodelled = "serializes state flags this extraction does not model: ";
        const string notAFlag = " rather than as the UInt8 a bool serializes to";

        (string Name, Func<List<(string, AssetValueType)>, List<(string, AssetValueType)>> Mutate, string? Want)[] cases =
        {
            ("the layout itself", flags => flags, null),
            ("a flag added", flags => Add(flags, ("HasContentsState", Checks.FlagValueType)),
                unmodelled + "added HasContentsState, missing none"),
            ("a flag added under an unknown name", flags => Add(flags, ("HasWidgetState", Checks.FlagValueType)),
                unmodelled + "added HasWidgetState, missing none"),
            ("a flag gone", flags => flags.Where(f => f.Item1 != "HasPowerState").ToList(),
                unmodelled + "added none, missing HasPowerState"),
            ("a flag renamed", flags => Add(flags.Where(f => f.Item1 != "HasLockState").ToList(), ("HasLatchState", Checks.FlagValueType)),
                unmodelled + "added HasLatchState, missing HasLockState"),
            ("nothing serialized", _ => new List<(string, AssetValueType)>(),
                unmodelled + "added none, missing " + string.Join(", ", Checks.ThingStateFields.OrderBy(n => n, StringComparer.Ordinal))),
            ("a flag widened", flags => Retype(flags, "HasModeState", AssetValueType.Int32),
                "serializes HasModeState as Int32" + notAFlag),
            ("a flag spelled Bool", flags => Retype(flags, "HasOpenState", AssetValueType.Bool),
                "serializes HasOpenState as Bool" + notAFlag),
            ("a flag serialized twice", flags => Add(flags, ("HasErrorState", Checks.FlagValueType)),
                "serializes more than one field named HasErrorState"),
            ("a flag added beside another widened",
                flags => Add(Retype(flags, "HasModeState", AssetValueType.Int32), ("HasWidgetState", Checks.FlagValueType)),
                unmodelled + "added HasWidgetState, missing none; serializes HasModeState as Int32" + notAFlag),
            ("a flag serialized twice beside another widened",
                flags => Add(Retype(flags, "HasModeState", AssetValueType.Int32), ("HasErrorState", Checks.FlagValueType)),
                "serializes more than one field named HasErrorState; serializes HasModeState as Int32" + notAFlag),
        };

        var failures = new List<string>();
        foreach ((string name, var mutate, string? want) in cases)
        {
            string? got = Checks.StateLayoutProblem(mutate(ModelledFlags()));
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The multi-problem rows hold the three answers to being asked together, and
    // are the only rows the separator between clauses is decided on: with one
    // clause every join spells the same thing.
    [Fact]
    public void DeclaredFlagsProblemHoldsTheFlagsToBeingSerializedBools()
    {
        const string enumType = "Assets.Scripts.Objects.OnOff";
        string every = Checks.JoinNames(Checks.ThingStateFields);

        (string Name, Func<Dictionary<string, Checks.DeclaredField>, Dictionary<string, Checks.DeclaredField>> Mutate, string? Want)[] cases =
        {
            ("every flag a bool", fields => fields, null),
            ("a flag no longer declared", fields => Drop(fields, "HasAccessState"),
                $"{Checks.ThingClass} declares no HasAccessState"),
            ("a flag turned into a byte-backed enum", fields => Set(fields, "HasOnOffState", enumType, serialized: true),
                $"{Checks.ThingClass} declares HasOnOffState as {enumType} rather than {Checks.FlagFieldType}"),
            ("a flag Unity stopped writing", fields => Set(fields, "HasModeState", Checks.FlagFieldType, serialized: false),
                $"{Checks.ThingClass} declares HasModeState where Unity does not serialize it"),
            ("the class emptied", _ => new Dictionary<string, Checks.DeclaredField>(StringComparer.Ordinal),
                $"{Checks.ThingClass} declares no {every}"),
            ("a flag gone beside another turned into an enum",
                fields => Set(Drop(fields, "HasAccessState"), "HasOnOffState", enumType, serialized: true),
                $"{Checks.ThingClass} declares no HasAccessState; " +
                $"declares HasOnOffState as {enumType} rather than {Checks.FlagFieldType}"),
            ("one of each at once",
                fields => Set(
                    Set(Drop(fields, "HasAccessState"), "HasOnOffState", enumType, serialized: true),
                    "HasModeState", Checks.FlagFieldType, serialized: false),
                $"{Checks.ThingClass} declares no HasAccessState; " +
                $"declares HasOnOffState as {enumType} rather than {Checks.FlagFieldType}; " +
                "declares HasModeState where Unity does not serialize it"),
        };

        var failures = new List<string>();
        foreach ((string name, var mutate, string? want) in cases)
        {
            string? got = Checks.DeclaredFlagsProblem(mutate(DeclaredFlags()));
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void DeclaredPowerProblemHoldsTheDrawToBeingSerializedOffDevice()
    {
        (string Name, Dictionary<string, Checks.DeclaredField> Fields, string? Want)[] cases =
        {
            ("declared as a float", Fields((Checks.PowerField, Checks.PowerFieldType, true)), null),
            ("renamed", Fields(("RequiredPower", Checks.PowerFieldType, true)),
                $"{Checks.DeviceClass} declares no {Checks.PowerField}"),
            ("moved off the class", Fields(), $"{Checks.DeviceClass} declares no {Checks.PowerField}"),
            ("widened to a double", Fields((Checks.PowerField, "System.Double", true)),
                $"declares {Checks.PowerField} as System.Double rather than {Checks.PowerFieldType}"),
            ("marked as not serialized", Fields((Checks.PowerField, Checks.PowerFieldType, false)),
                $"declares {Checks.PowerField} where Unity does not serialize it"),
        };

        var failures = new List<string>();
        foreach ((string name, var fields, string? want) in cases)
        {
            string? got = Checks.DeclaredPowerProblem(fields);
            if (want == null ? got != null : got == null || !got.Contains(want, StringComparison.Ordinal))
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void PowerCoverageProblemRefusesARosterWithNoDrawOnIt()
    {
        Assert.Null(Checks.PowerCoverageProblem(239, 1565));
        Assert.Null(Checks.PowerCoverageProblem(1, 1565));
        Assert.Contains(
            $"none of the 1565 prefabs read carries a {Checks.PowerField}",
            Checks.PowerCoverageProblem(0, 1565),
            StringComparison.Ordinal);
    }

    // A roster where no prefab declares a slot is not a build whose things hold
    // nothing, it is a build whose slot list this reader stopped finding filled.
    // The single-slotted row is the smallest the check accepts, which a check
    // asking for a share rather than a count would refuse.
    [Fact]
    public void SlotCoverageProblemRefusesARosterWithNoSlotOnIt()
    {
        (string Name, int Declaring, int Total, string? Want)[] cases =
        {
            ("a roster where most things hold nothing", 74, 1565, null),
            ("a roster with a single slotted thing", 1, 1565, null),
            ("every thing on the roster slotted", 1565, 1565, null),
            ("a roster that fills the list nowhere", 0, 1565, "none of the 1565 prefabs read declares a slot"),
            ("a roster with nothing on it", 0, 0, "none of the 0 prefabs read declares a slot"),
        };

        var failures = new List<string>();
        foreach ((string name, int declaring, int total, string? want) in cases)
        {
            string? got = Checks.SlotCoverageProblem(declaring, total);
            if (want == null ? got != null : got == null || !got.Contains(want, StringComparison.Ordinal))
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A problem spelled as an empty string joins to nothing, so a join reading
    // the length of what it produced would answer that there was no problem.
    [Fact]
    public void JoinProblemsNamesEveryProblemAtOnce()
    {
        (string Name, string?[] Problems, string? Want)[] cases =
        {
            ("nothing asked", Array.Empty<string>(), null),
            ("no problem at all", new string?[] { null, null }, null),
            ("the first of two", new[] { "the flags moved", null }, "the flags moved"),
            ("the second of two", new[] { null, "the draw moved" }, "the draw moved"),
            ("both of two", new[] { "the flags moved", "the draw moved" }, "the flags moved; the draw moved"),
            ("three at once", new[] { "one", "two", "three" }, "one; two; three"),
            ("a problem spelled as nothing", new[] { "" }, ""),
            ("a problem spelled as nothing beside one that is not", new[] { "", "the draw moved" }, "; the draw moved"),
        };

        var failures = new List<string>();
        foreach ((string name, string?[] problems, string? want) in cases)
        {
            string? got = Checks.JoinProblems(problems);
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The spelling is part of the value: two packages covering builds the three
    // numbers agree on are two things, and an ordering blind to that leaves a
    // sorted set one member short of the hash set beside it. The text is
    // compared only where the numbers cannot tell two builds apart.
    [Fact]
    public void EngineVersionOrdersByTheNumbersAndSettlesATieOnTheText()
    {
        Checks.EngineVersion Version(int major, int minor, int patch, string? text = null) =>
            new(major, minor, patch, text ?? $"Unity {major}.{minor}.{patch}f1");

        (string Name, Checks.EngineVersion Left, Checks.EngineVersion Right, int Want)[] cases =
        {
            ("one build against itself", Version(2022, 3, 41), Version(2022, 3, 41), 0),
            ("a major release apart", Version(2021, 3, 41), Version(2022, 3, 41), -1),
            ("a minor release apart", Version(2022, 4, 5), Version(2022, 3, 41), 1),
            ("a patch apart", Version(2022, 3, 41), Version(2022, 3, 42), -1),
            ("two spellings of one set of numbers",
                Version(2022, 3, 41, "Unity 2022.3.41b2"), Version(2022, 3, 41, "Unity 2022.3.41f1"), -1),
            ("a header that is not a version against one that is",
                Version(0, 0, 0, "no engine at all"), Version(0, 0, 0, "the text \"steamdepot\""), -1),
        };

        var failures = new List<string>();
        foreach ((string name, var left, var right, int want) in cases)
        {
            int got = Math.Sign(left.CompareTo(right));
            if (got != want)
            {
                failures.Add($"{name}: compared {got}, want {want}");
            }
            if (Math.Sign(right.CompareTo(left)) != -want)
            {
                failures.Add($"{name}: reversed compared {Math.Sign(right.CompareTo(left))}, want {-want}");
            }
            if (left.Equals(right) != (got == 0))
            {
                failures.Add($"{name}: compared {got} and answered equal {left.Equals(right)}, want one answer");
            }
        }

        Checks.EngineVersion newest = new[]
        {
            Version(2022, 3, 41, "a"),
            Version(2021, 3, 14, "z"),
        }.Max();
        if (newest != Version(2022, 3, 41, "a"))
        {
            failures.Add($"the newest of two: took {newest.Text}, want the greatest numbers rather than the greatest text");
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The caught types are ones the libraries were seen to raise against real bad
    // inputs, not ones read off the type hierarchy, and they share no common
    // base. The uncaught rows are equally what a defect in this reader looks
    // like from outside; Checks.SerializedFileProblem draws that line instead.
    [Fact]
    public void UnopenableTellsABuildInputThatIsWrongFromAFailureNothingAnticipated()
    {
        (string Name, Exception Failure, bool Want)[] cases =
        {
            ("a file that ends before the reader is done with it", new EndOfStreamException(), true),
            ("a file that is not there after all", new FileNotFoundException(), true),
            ("a read that failed some other way", new IOException(), true),
            ("a file that is not of that format at all", new NotSupportedException(), true),
            ("a file the process may not open", new UnauthorizedAccessException(), true),
            ("a file that is not a PE", new BadImageFormatException(), true),
            ("a machine too small for the file", new OutOfMemoryException(), false),
            // In System.IO but not an IOException. No reader here was seen to
            // raise it; a list assembled by reading names would carry it.
            ("bytes that are not what a header said", new InvalidDataException(), false),
            ("a decision this reader made", new RefusalException("the flags moved"), false),
            ("a reader that dereferenced nothing", new NullReferenceException(), false),
            ("a library asked for something it does not do in that state", new InvalidOperationException(), false),
            ("text that is not a number", new FormatException(), false),
            ("a number wider than what holds it", new OverflowException(), false),
            // The two the asset reader was seen to raise for a malformed
            // serialized file, and the base one of them shares. A list naming
            // the base would swallow both without naming either.
            ("an argument this reader got wrong", new ArgumentException(), false),
            ("a length taken out of a file that was not one", new ArgumentOutOfRangeException(), false),
            ("an index past the end of a table", new IndexOutOfRangeException(), false),
        };

        var failures = new List<string>();
        foreach ((string name, var failure, bool want) in cases)
        {
            bool got = Checks.Unopenable(failure);
            if (got != want)
            {
                failures.Add($"{name}: {failure.GetType().FullName} read as {(got ? "the input being wrong" : "unanticipated")}, want the other");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Every row above is an exception this file built, so none asks the library
    // anything. The library refuses a package in a format it cannot read with a
    // bare Exception, the one type no list can name without naming every failure
    // there is, which is why ClassPackageFileProblem reads the version first.
    [Fact]
    public void TheAssetLibraryRefusesAWrongClassPackageAsTheListAboveExpects()
    {
        Exception? wrongFormat = Raised(NotAPackage());
        Assert.NotNull(wrongFormat);
        Assert.True(Checks.Unopenable(wrongFormat));

        Exception newer = Assert.IsType<Exception>(Raised(NewerPackage()));
        Assert.False(Checks.Unopenable(newer));
    }

    // The bytes go over as a stream rather than to disk, the library reading a
    // package the same way either side of that.
    private static Exception? Raised(byte[] bytes)
    {
        try
        {
            new AssetsManager().LoadClassPackage(new MemoryStream(bytes, writable: false));
        }
        catch (Exception failure)
        {
            return failure;
        }
        return null;
    }

    // What a download that saved an error page leaves under this name.
    private static byte[] NotAPackage() =>
        Encoding.UTF8.GetBytes("<html><body>404 not found</body></html>");

    // A real header naming a format version past the newest this reader reads.
    private static byte[] NewerPackage()
    {
        var bytes = new byte[Checks.PackageHeader.Size];
        Encoding.UTF8.GetBytes(Checks.PackageHeader.Magic).CopyTo(bytes, 0);
        bytes[Checks.PackageHeader.VersionAt] = Checks.PackageHeader.HighestVersion + 1;
        return bytes;
    }

    // The serialized file has no magic number, and the asset reader opens most
    // malformed files without a word, so a file refused here is one nothing
    // downstream would have refused at all. A partial download keeps a whole,
    // honest header naming a length the rest of the file no longer reaches.
    [Fact]
    public void SerializedFileProblemHoldsTheFileToWhatItSaysItIs()
    {
        const string path = "/depot/Stationeers_Data/resources.assets";
        const string noRoster = "so there is no prefab roster to read";

        string Refusal(string clauses) => $"{path}: this file {clauses}, {noRoster}";

        Checks.SerializedExtent Extent(long fileSize, long dataOffset) => new(fileSize, dataOffset);

        (string Name, Checks.SerializedExtent? Header, long Length, string? Want)[] cases =
        {
            ("a file that is the length it declares", Extent(23232, 4096), 23232, null),
            ("a depot file of the size the game ships", Extent(734003200, 262144), 734003200, null),
            ("asset data starting at the last byte there is", Extent(23232, 23232), 23232, null),
            ("a file with bytes past the end it declares", Extent(23232, 4096), 23300, null),
            ("a download that stopped part way", Extent(734003200, 262144), 512000000,
                Refusal("declares itself 734003200 bytes and is 512000000")),
            ("a download that stopped one byte short", Extent(23232, 4096), 23231,
                Refusal("declares itself 23232 bytes and is 23231")),
            ("a file declaring no length at all", Extent(0, 32), 64,
                Refusal("declares itself 0 bytes and is 64; puts its asset data at offset 32 in the 0 bytes it declares")),
            // Sixty-four bits of something that is not a length read as one.
            ("a file declaring a length below zero", Extent(-6076574518398440533, 48), 84,
                Refusal("declares itself -6076574518398440533 bytes and is 84; " +
                    "puts its asset data at offset 48 in the -6076574518398440533 bytes it declares")),
            ("a file whose asset data starts nowhere", Extent(23232, 0), 23232,
                Refusal("puts its asset data at offset 0 in the 23232 bytes it declares")),
            ("a file whose asset data starts past its own end", Extent(23232, 268435455), 23232,
                Refusal("puts its asset data at offset 268435455 in the 23232 bytes it declares")),
            ("a file whose asset data starts below zero", Extent(23232, -5), 23232,
                Refusal("puts its asset data at offset -5 in the 23232 bytes it declares")),
            ("a file whose asset data sits past the length it declares and inside the file it is",
                Extent(4096, 8000), 10000,
                Refusal("puts its asset data at offset 8000 in the 4096 bytes it declares")),
            ("a file with nothing at the front to make a claim with", null, 12,
                $"{path}: 12 bytes, which is not even a serialized file header, {noRoster}"),
            ("a file with nothing in it at all", null, 0,
                $"{path}: 0 bytes, which is not even a serialized file header, {noRoster}"),
            ("a file that is neither the length it declares nor holds data where it says",
                Extent(99999, 0), 64,
                Refusal("declares itself 99999 bytes and is 64; puts its asset data at offset 0 in the 99999 bytes it declares")),
        };

        var failures = new List<string>();
        foreach ((string name, var header, long length, string? want) in cases)
        {
            string? got = Checks.SerializedFileProblem(header, length, path, noRoster);
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Fed a package truncated to half its length the asset library read one
    // without complaining, answered with all 1008 engine versions the whole
    // package covers, and built a database naming 321 classes -- so both checks
    // behind this one pass while the walk reads layouts taken out of zeros.
    [Fact]
    public void ClassPackageFileProblemHoldsThePackageToWhatItSaysItIs()
    {
        const string path = "/opt/classdata.tpk";
        const string noTypes = "so nothing here describes the engine types the serialized files were written by";

        string Refusal(string clauses) => $"{path}: this file {clauses}, {noTypes}";

        Checks.PackageExtent Extent(byte version, uint compressedSize) => new(version, compressedSize);

        (string Name, Checks.PackageExtent? Header, long Length, string? Want)[] cases =
        {
            ("the package the build pins", Extent(1, 289585), 289605, null),
            ("a package written in the oldest version there is", Extent(0, 289585), 289605, null),
            ("a package with bytes past the body it declares", Extent(1, 289585), 289605 + 4096, null),
            ("a download that stopped part way", Extent(1, 289585), 144812,
                Refusal("declares 289585 bytes of class data behind its 20-byte header and is 144812")),
            ("a download that stopped one byte short", Extent(1, 289585), 289604,
                Refusal("declares 289585 bytes of class data behind its 20-byte header and is 289604")),
            ("a download that stopped at the end of the header", Extent(1, 289585), 20,
                Refusal("declares 289585 bytes of class data behind its 20-byte header and is 20")),
            // An empty body has nothing to do with how long the file is, so the
            // sentence names no length. The second row keeps the two apart.
            ("a package declaring no class data at all", Extent(1, 0), 289605,
                Refusal("declares no class data at all behind its 20-byte header")),
            ("a package declaring no class data in a file that is only the header", Extent(1, 0), 20,
                Refusal("declares no class data at all behind its 20-byte header")),
            // The library casts those same bytes to a signed int and asks for
            // minus sixteen of them, arriving as an argument out of range.
            ("a package declaring more class data than the field can count", Extent(1, 4294967280), 289605,
                Refusal("declares 4294967280 bytes of class data behind its 20-byte header and is 289605")),
            // The size is carried unsigned, so this is the far side of the gate.
            // It is also the row the width of the sum turns on: added to the
            // 20-byte header in the width the two have rather than the one they
            // need, it comes back as nineteen and is let past.
            ("a package declaring a body of every size the field can spell", Extent(1, uint.MaxValue), 289605,
                Refusal($"declares {uint.MaxValue} bytes of class data behind its 20-byte header and is 289605")),
            ("a package written in a version newer than the reader reads", Extent(2, 289585), 289605,
                Refusal("is written in class package format version 2, and this reader reads up to 1")),
            ("a package whose version byte is not one at all", Extent(255, 289585), 289605,
                Refusal("is written in class package format version 255, and this reader reads up to 1")),
            ("a newer package that is also not all there", Extent(2, 289585), 144812,
                Refusal("is written in class package format version 2, and this reader reads up to 1; " +
                    "declares 289585 bytes of class data behind its 20-byte header and is 144812")),
            ("a file carrying no class package magic", null, 54, null),
            ("a file with nothing in it at all", null, 0, null),
        };

        var failures = new List<string>();
        foreach ((string name, var header, long length, string? want) in cases)
        {
            string? got = Checks.ClassPackageFileProblem(header, length, path, noTypes);
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A major at or below zero orders at or below every version any package
    // could describe, so without a gate the answer would be null for the same
    // reason it is null for a package that does describe the build.
    [Fact]
    public void ClassPackageEngineProblemRefusesAPackageOlderThanTheBuild()
    {
        Checks.EngineVersion Version(int major, int minor, int patch) =>
            new(major, minor, patch, $"Unity {major}.{minor}.{patch}f1");

        Checks.EngineVersion[] covered =
        {
            Version(2021, 3, 14),
            Version(2022, 3, 41),
            Version(2022, 1, 9),
        };

        const string leftOut = ", so every engine type that moved since would be left out of the database rather than reported";
        const string notAVersion = ", which is not an engine version this reader can order a class package against";
        const string describesNothing = "the class package describes no engine version at all, so it cannot describe the engine the serialized files name, ";
        var unversioned = new Checks.EngineVersion(0, 0, 0, "Unity 0.0.0");

        (string Name, Checks.EngineVersion Wanted, Checks.EngineVersion[] Covered, string? Want)[] cases =
        {
            ("the newest version described", Version(2022, 3, 41), covered, null),
            ("a version in the middle", Version(2022, 1, 9), covered, null),
            ("older than everything described", Version(2019, 4, 1), covered, null),
            ("one patch newer than the newest", Version(2022, 3, 42), covered,
                "the serialized files were written by Unity 2022.3.42f1, newer than the Unity 2022.3.41f1 the class package describes" + leftOut),
            ("a major release newer", Version(6000, 0, 25), covered,
                "the serialized files were written by Unity 6000.0.25f1, newer than the Unity 2022.3.41f1 the class package describes" + leftOut),
            // Unity ships the minor number as the release stream. These two rows
            // are the only ones where it decides the comparison, so without them
            // a package a whole stream too old is accepted.
            ("a minor release newer than the newest described", Version(2022, 4, 5), covered,
                "the serialized files were written by Unity 2022.4.5f1, newer than the Unity 2022.3.41f1 the class package describes" + leftOut),
            ("an older minor release, patched past the newest", Version(2022, 2, 99), covered, null),
            ("a package describing nothing", Version(2022, 3, 41), Array.Empty<Checks.EngineVersion>(),
                describesNothing + "Unity 2022.3.41f1"),
            ("serialized files naming a zero version", unversioned, covered,
                "the serialized files name Unity 0.0.0" + notAVersion),
            ("serialized files naming an engine below every version described",
                new Checks.EngineVersion(0, 3, 41, "Unity 0.3.41f1"),
                covered,
                "the serialized files name Unity 0.3.41f1" + notAVersion),
            // A header left unset and a header carrying text that is not a
            // version both arrive as this major. Without these two rows a caller
            // could narrow the gate back to the zero version unnoticed.
            ("serialized files naming no engine at all",
                new Checks.EngineVersion(0, 0, 0, "no engine at all"), covered,
                "the serialized files name no engine at all" + notAVersion),
            ("serialized files naming text that is not a version",
                new Checks.EngineVersion(0, 0, 0, "the text \"steamdepot\""), covered,
                "the serialized files name the text \"steamdepot\"" + notAVersion),
            // The shape a header spelling a number with a sign parses to. A gate
            // asking only about zero takes it for an older build and passes.
            ("serialized files naming a version below zero",
                new Checks.EngineVersion(-1, 2, 3, "Unity -1.2.3"), covered,
                "the serialized files name Unity -1.2.3" + notAVersion),
            ("neither the files nor the package naming an engine", unversioned, Array.Empty<Checks.EngineVersion>(),
                "the serialized files name Unity 0.0.0" + notAVersion + "; " + describesNothing + "Unity 0.0.0"),
        };

        var failures = new List<string>();
        foreach ((string name, var wanted, var packaged, string? want) in cases)
        {
            string? got = Checks.ClassPackageEngineProblem(wanted, packaged);
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The floor is spelled as the literal 1000 rather than Checks.MinPrefabs: an
    // expectation derived from the constant holds for any floor, including 1.
    [Fact]
    public void ChooseRosterTakesTheOneSelfContainedCandidate()
    {
        (string Name, (long PathId, int External, int Total)[] Candidates, int Seen, int Want, string? WantProblem)[] cases =
        {
            ("one self-contained roster",
                new[] { (7L, 3, 1565), (9L, 0, 1565) }, 2, 1, null),
            ("one self-contained roster exactly at the floor",
                new[] { (9L, 0, 1000) }, 1, 0, null),
            ("one self-contained roster one below the floor",
                new[] { (9L, 0, 999) }, 1, 0, "carries 999 prefabs, want at least 1000"),
            ("an empty roster, self-contained by vacuity",
                new[] { (9L, 0, 0) }, 1, 0, "carries 0 prefabs, want at least 1000"),
            ("two self-contained rosters",
                new[] { (7L, 0, 1565), (9L, 0, 1565) }, 2, 0, "holds 2 self-contained"),
            ("no self-contained roster",
                new[] { (7L, 3, 1565) }, 1, 0, "pathId 7: 3 of 1565 entries point outside resources.assets"),
            ("the field renamed",
                Array.Empty<(long, int, int)>(), 4, 0, $"none of the 4 {Checks.WorldManagerClass} in resources.assets carries SourcePrefabs"),
            ("the class renamed",
                Array.Empty<(long, int, int)>(), 0, 0, $"resources.assets holds no {Checks.WorldManagerClass} at all"),
        };

        var failures = new List<string>();
        foreach ((string name, var candidates, int seen, int want, string? wantProblem) in cases)
        {
            string? got = Checks.ChooseRoster(candidates, "resources.assets", seen, out int chosen);
            if (wantProblem == null)
            {
                if (got != null)
                {
                    failures.Add($"{name}: got {Show(got)}, want index {want}");
                }
                else if (chosen != want)
                {
                    failures.Add($"{name}: chose index {chosen}, want {want}");
                }
            }
            else if (got == null || !got.Contains(wantProblem, StringComparison.Ordinal))
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(wantProblem)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The four rows carrying one part apiece make each term of the conjunction
    // decide; without them a check asking about only three agrees on every
    // version above. Deciding and spelling are two members, so the spelling is
    // taken on every row the refusal does not fire on.
    [Fact]
    public void AssemblyVersionProblemRefusesAStrippedVersionResource()
    {
        (string Name, int Major, int Minor, int Build, int Revision, string? Want)[] cases =
        {
            ("a stamped build", 0, 2, 5445, 24403, "0.2.5445.24403"),
            ("a build with every part set", 1, 2, 3, 4, "1.2.3.4"),
            ("only a major", 5, 0, 0, 0, "5.0.0.0"),
            ("only a minor", 0, 2, 0, 0, "0.2.0.0"),
            ("only a build number", 0, 0, 5445, 0, "0.0.5445.0"),
            ("only a revision", 0, 0, 0, 1, "0.0.0.1"),
            ("a resource stamped with no build at all", 0, 0, 0, 0, null),
        };

        var failures = new List<string>();
        foreach ((string name, int major, int minor, int build, int revision, string? want) in cases)
        {
            var stamp = new Checks.FileVersion(major, minor, build, revision);
            string? problem = Checks.AssemblyVersionProblem(stamp, "Assembly-CSharp.dll");
            if (want == null)
            {
                string refusal =
                    $"Assembly-CSharp.dll is stamped 0.0.0.0 in its {VersionResource.Name}, {Checks.NoBuild}";
                if (problem != refusal)
                {
                    failures.Add($"{name}: got {Show(problem)}, want {Show(refusal)}");
                }
                continue;
            }

            if (problem != null)
            {
                failures.Add($"{name}: got {Show(problem)}, want {Show(want)}");
                continue;
            }

            string? got = Checks.AssemblyVersion(stamp);
            if (got != want)
            {
                failures.Add($"{name}: spelled {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A game update that turned the draw into something JSON has no number for
    // turned it on every device at once, so the check runs over the whole
    // roster. A sentence with one clause per prefab is no more use than its
    // count and its first few, so the refusal names a bounded sample.
    [Fact]
    public void DrawsProblemNamesEveryDrawTheArtifactCannotHold()
    {
        const string carries = "the roster carries ";
        const string noNumber = " values the artifact has no number for: ";

        (string Name, (string Where, float Draw)[] Draws, string? Want)[] cases =
        {
            ("a roster with no draw on it", Array.Empty<(string, float)>(), null),
            ("every draw a number", new[] { ("the prefab A at pathId 1", 0f), ("the prefab B at pathId 2", 0.1f) }, null),
            ("one draw that is not a number",
                new[] { ("the prefab A at pathId 1", 10f), ("the prefab B at pathId 2", float.NaN) },
                $"{carries}1 of 2 {Checks.PowerField}{noNumber}the prefab B at pathId 2 carries NaN"),
            ("an unbounded draw",
                new[] { ("the prefab A at pathId 1", float.PositiveInfinity) },
                $"{carries}1 of 1 {Checks.PowerField}{noNumber}the prefab A at pathId 1 carries Infinity"),
            ("an unbounded draw below zero",
                new[] { ("the prefab A at pathId 1", float.NegativeInfinity) },
                $"{carries}1 of 1 {Checks.PowerField}{noNumber}the prefab A at pathId 1 carries -Infinity"),
            ("two of three, the first and the last",
                new[]
                {
                    ("the prefab A at pathId 1", float.NaN),
                    ("the prefab B at pathId 2", 10f),
                    ("the prefab C at pathId 3", float.NegativeInfinity),
                },
                $"{carries}2 of 3 {Checks.PowerField}{noNumber}" +
                "the prefab A at pathId 1 carries NaN, the prefab C at pathId 3 carries -Infinity"),
            // The boundary the count is decided at. Without it, a sample naming
            // its five and withholding nothing reads as one claiming none were.
            ("exactly as many bad draws as a refusal names",
                Enumerable.Range(1, 5).Select(n => ($"the prefab P{n} at pathId {n}", float.NaN)).ToArray(),
                $"{carries}5 of 5 {Checks.PowerField}{noNumber}" +
                "the prefab P1 at pathId 1 carries NaN, the prefab P2 at pathId 2 carries NaN, " +
                "the prefab P3 at pathId 3 carries NaN, the prefab P4 at pathId 4 carries NaN, " +
                "the prefab P5 at pathId 5 carries NaN"),
            ("more bad draws than a refusal names",
                Enumerable.Range(1, 7).Select(n => ($"the prefab P{n} at pathId {n}", float.NaN)).ToArray(),
                $"{carries}7 of 7 {Checks.PowerField}{noNumber}" +
                "the prefab P1 at pathId 1 carries NaN, the prefab P2 at pathId 2 carries NaN, " +
                "the prefab P3 at pathId 3 carries NaN, the prefab P4 at pathId 4 carries NaN, " +
                "the prefab P5 at pathId 5 carries NaN, and 2 more"),
        };

        var failures = new List<string>();
        foreach ((string name, var draws, string? want) in cases)
        {
            string? got = Checks.DrawsProblem(draws);
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A game update that moved a field moved it on every prefab carrying it, and
    // one that moved two lands them on different prefabs, so a depot assessed at
    // the first bad entry is a depot assessed once per bad entry.
    [Fact]
    public void RosterProblemNamesEveryPrefabTheWalkRefused()
    {
        const string refused = "the walk refused ";

        (string Name, string[] Refused, int Total, string? Want)[] cases =
        {
            ("a roster the walk read all of", Array.Empty<string>(), 1565, null),
            ("a roster with nothing on it", Array.Empty<string>(), 0, null),
            ("one entry of many",
                new[] { "the prefab StructureSensor at pathId 12 names no script type" }, 1565,
                $"{refused}1 of the 1565 prefabs on the roster: the prefab StructureSensor at pathId 12 names no script type"),
            ("two entries far apart",
                new[]
                {
                    "the prefab StructureSensor at pathId 12 names no script type",
                    "the prefab ItemWrench at pathId 900 serializes state flags this extraction does not model",
                },
                1565,
                $"{refused}2 of the 1565 prefabs on the roster: " +
                "the prefab StructureSensor at pathId 12 names no script type, " +
                "the prefab ItemWrench at pathId 900 serializes state flags this extraction does not model"),
            ("every entry on a small roster",
                Enumerable.Range(1, 3).Select(n => $"the prefab P{n} at pathId {n} names no script type").ToArray(), 3,
                $"{refused}3 of the 3 prefabs on the roster: " +
                "the prefab P1 at pathId 1 names no script type, the prefab P2 at pathId 2 names no script type, " +
                "the prefab P3 at pathId 3 names no script type"),
            // The boundary the count is decided at, as for the draws above.
            ("exactly as many refusals as a sentence names",
                Enumerable.Range(1, 5).Select(n => $"the prefab P{n} at pathId {n} names no script type").ToArray(), 1565,
                $"{refused}5 of the 1565 prefabs on the roster: " +
                "the prefab P1 at pathId 1 names no script type, the prefab P2 at pathId 2 names no script type, " +
                "the prefab P3 at pathId 3 names no script type, the prefab P4 at pathId 4 names no script type, " +
                "the prefab P5 at pathId 5 names no script type"),
            ("more refusals than a sentence names",
                Enumerable.Range(1, 7).Select(n => $"the prefab P{n} at pathId {n} names no script type").ToArray(), 1565,
                $"{refused}7 of the 1565 prefabs on the roster: " +
                "the prefab P1 at pathId 1 names no script type, the prefab P2 at pathId 2 names no script type, " +
                "the prefab P3 at pathId 3 names no script type, the prefab P4 at pathId 4 names no script type, " +
                "the prefab P5 at pathId 5 names no script type, and 2 more"),
            // A MonoScript that will not deserialize is refused once per prefab
            // it drives, in the same sentence. The count stays the count of
            // prefabs; the sample names the failure once, because five copies of
            // one sentence crowd out every other clause.
            ("one failure met once per prefab it was met on",
                Enumerable.Repeat("script 0:12 does not deserialize", 900).ToArray(), 1565,
                $"{refused}900 of the 1565 prefabs on the roster: script 0:12 does not deserialize"),
            ("a repeated failure beside others",
                new[] { "script 0:12 does not deserialize" }
                    .Concat(Enumerable.Range(1, 5).Select(n => $"the prefab P{n} at pathId {n} names no script type"))
                    .Concat(Enumerable.Repeat("script 0:12 does not deserialize", 3))
                    .ToArray(),
                1565,
                $"{refused}9 of the 1565 prefabs on the roster: script 0:12 does not deserialize, " +
                "the prefab P1 at pathId 1 names no script type, the prefab P2 at pathId 2 names no script type, " +
                "the prefab P3 at pathId 3 names no script type, the prefab P4 at pathId 4 names no script type, " +
                "and 1 more"),
        };

        var failures = new List<string>();
        foreach ((string name, string[] entries, int total, string? want) in cases)
        {
            string? got = Checks.RosterProblem(entries, total);
            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The walk builds this while it still has the asset and the roster checks
    // build it again off what the walk produced, and a refusal naming a prefab
    // two ways cannot be grepped against the artifact. The nameless spelling is
    // for a prefab placed but not read far enough to be called anything.
    [Fact]
    public void PrefabWhereNamesAPrefabTheWayEveryRefusalDoes()
    {
        Assert.Equal("the prefab StructureSensor at pathId 42", Checks.PrefabWhere("StructureSensor", 42));
        Assert.Equal("the prefab ItemWrench at pathId -1", Checks.PrefabWhere("ItemWrench", -1));
        Assert.Equal("the prefab at pathId 42", Checks.PrefabWhere(42));
        Assert.Equal("the prefab at pathId -1", Checks.PrefabWhere(-1));
    }

    [Fact]
    public void JoinNamesOrdersOrdinallyAndNamesAnEmptySet()
    {
        Assert.Equal("none", Checks.JoinNames(Array.Empty<string>()));
        Assert.Equal("HasErrorState, HasPowerState, HasZebraState", Checks.JoinNames(new[] { "HasZebraState", "HasErrorState", "HasPowerState" }));
        Assert.Equal("Alpha, alpha", Checks.JoinNames(new[] { "alpha", "Alpha" }));
    }

    private static List<(string, AssetValueType)> ModelledFlags() =>
        Checks.ThingStateFields.Select(name => (name, Checks.FlagValueType)).ToList();

    private static Dictionary<string, Checks.DeclaredField> DeclaredFlags() =>
        Checks.ThingStateFields.ToDictionary(
            name => name, _ => new Checks.DeclaredField(Checks.FlagFieldType, true), StringComparer.Ordinal);

    private static List<(string, AssetValueType)> Add(List<(string, AssetValueType)> flags, (string, AssetValueType) extra) =>
        new(flags) { extra };

    private static List<(string, AssetValueType)> Retype(
        List<(string, AssetValueType)> flags, string name, AssetValueType valueType) =>
        flags.Select(flag => flag.Item1 == name ? (name, valueType) : flag).ToList();

    private static Dictionary<string, Checks.DeclaredField> Drop(IReadOnlyDictionary<string, Checks.DeclaredField> fields, string name)
    {
        var copy = new Dictionary<string, Checks.DeclaredField>(fields, StringComparer.Ordinal);
        copy.Remove(name);
        return copy;
    }

    private static Dictionary<string, Checks.DeclaredField> Set(
        IReadOnlyDictionary<string, Checks.DeclaredField> fields, string name, string type, bool serialized)
    {
        return new Dictionary<string, Checks.DeclaredField>(fields, StringComparer.Ordinal)
        {
            [name] = new Checks.DeclaredField(type, serialized),
        };
    }

    private static Dictionary<string, Checks.DeclaredField> Fields(params (string Name, string Type, bool Serialized)[] fields) =>
        fields.ToDictionary(field => field.Name, field => new Checks.DeclaredField(field.Type, field.Serialized), StringComparer.Ordinal);

    private static string Show(string? value) => value == null ? "null" : $"\"{value}\"";
}
