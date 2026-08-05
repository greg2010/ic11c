using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Json;
using AssetsTools.NET;
using AssetsTools.NET.Extra;
using Xunit;

namespace PrefabReader.Tests;

// WalkTests builds asset fields here rather than reading a serialized file, so
// the walk runs on a machine with no game depot. The two failures it refuses
// differ: a field at the wrong width answers (a byte read as a float is the
// byte), while a field that is not there throws NullReferenceException.
public class WalkTests : TempDirectories
{
    [Fact]
    public void BuildPrefabReadsWhatTheRosterCarries()
    {
        Program.Prefab prefab = Program.BuildPrefab(Asset(), "Assets.Scripts.Objects.Pipes.Sensor", 42);

        Assert.Equal("StructureSensor", prefab.Name);
        Assert.Equal(1812372242, prefab.Hash);
        Assert.Equal("Assets.Scripts.Objects.Pipes.Sensor", prefab.Script);
        Assert.Equal(10f, prefab.UsedPower);
        Assert.Equal(new ushort[] { 2, 5, 2 }, prefab.Slots.Select(slot => slot.Class));
        Assert.Equal(Checks.StateFields.OrderBy(name => name, StringComparer.Ordinal), prefab.State.Keys.OrderBy(name => name, StringComparer.Ordinal));
        Assert.True(prefab.State["HasPowerState"]);
        Assert.False(prefab.State["HasOpenState"]);
    }

    // tools/isagen looks each entry up by the class its script names, so an
    // entry carrying none arrives there as a prefab driven by a class that is
    // not there, with nothing to say the reader never read one.
    [Fact]
    public void BuildPrefabRefusesAPrefabNamingNoScriptType()
    {
        Assert.Equal(
            "the prefab StructureSensor at pathId 42 names no script type",
            Assert.IsType<RefusalException>(Record.Exception(() => Program.BuildPrefab(Asset(), null, 42))).Message);
    }

    // The game update that drives a prefab off a class this reader cannot name
    // is the kind that moves what that class serializes, so both questions are
    // asked of one deserialized prefab together.
    [Fact]
    public void BuildPrefabNamesTheScriptAndTheLayoutAtOnce()
    {
        AssetTypeValueField asset = Asset(fields => fields.Add(Flag("HasWidgetState", true)));

        Assert.Equal(
            "the prefab StructureSensor at pathId 42 names no script type; " +
            "serializes state flags this extraction does not model: added HasWidgetState, missing none",
            Assert.IsType<RefusalException>(Record.Exception(() => Program.BuildPrefab(asset, null, 42))).Message);
    }

    // A field that is not there says the layout moved. A field there holding
    // nothing is a roster entry the tables downstream are keyed by nothing at
    // all, and is also what every other refusal here would name the prefab as,
    // so the two cannot be reported in the same sentence.
    [Fact]
    public void BuildPrefabTellsANameThatIsNotThereFromOneThatIsEmpty()
    {
        (string Name, Action<List<AssetTypeValueField>> Mutate, string? Want, string? WantProblem)[] cases =
        {
            ("a named prefab", _ => { }, "StructureSensor", null),
            ("a name that is not there", fields => Remove(fields, "PrefabName"), null, "has no PrefabName field"),
            ("a name that is empty", fields => Replace(fields, Str("PrefabName", "")), null,
                "the prefab at pathId 42 carries an empty PrefabName"),
        };

        var failures = new List<string>();
        foreach ((string name, var mutate, string? want, string? wantProblem) in cases)
        {
            Program.Prefab? prefab = null;
            Exception? thrown = Record.Exception(() => prefab = Build(mutate));
            if (wantProblem != null)
            {
                if (thrown is not RefusalException || !thrown.Message.Contains(wantProblem, StringComparison.Ordinal))
                {
                    failures.Add($"{name}: got {thrown?.Message ?? $"the prefab {prefab!.Name}"}, want {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want {want}");
            }
            else if (prefab!.Name != want)
            {
                failures.Add($"{name}: read as {prefab.Name}, want {want}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void BuildPrefabTellsAnAbsentDrawFromAZeroOne()
    {
        Assert.Null(Build(fields => Remove(fields, Checks.PowerField)).UsedPower);
        Assert.Equal(0f, Build(fields => Replace(fields, Float(Checks.PowerField, 0f))).UsedPower);
    }

    // The library's conversions do not refuse a width they were not asked for,
    // and every other draw this suite reads is a whole number that survives a
    // narrower one. 0.1f narrows to zero, and the game grants
    // LogicType.RequiredPower on a draw above zero.
    [Fact]
    public void BuildPrefabKeepsTheFractionOfADraw()
    {
        Assert.Equal(0.1f, Build(fields => Replace(fields, Float(Checks.PowerField, 0.1f))).UsedPower);
    }

    // The game derives the hash from the prefab name and keeps it in an int, and
    // roughly half the shipped roster comes out below zero. The read and the
    // write are both held to the signed width, because they could lose the sign
    // independently and only the written number reaches a user.
    [Fact]
    public void TheWholePrefabHashReachesTheArtifact()
    {
        int[] hashes = { int.MinValue, -1886261558, -1, 0, 1, 1812372242, int.MaxValue };

        var failures = new List<string>();
        foreach (int hash in hashes)
        {
            Exception? thrown = Record.Exception(() =>
            {
                Program.Prefab prefab = Build(fields => Replace(fields, Int("PrefabHash", hash)));
                if (prefab.Hash != hash)
                {
                    failures.Add($"the hash {hash}: read as {prefab.Hash}");
                }

                string path = TempPath("prefabs.json");
                Program.Write(path, "0.2.5445.24403", new[] { prefab });
                using JsonDocument written = JsonDocument.Parse(File.ReadAllBytes(path));
                // Compared as text rather than through GetInt32, which throws
                // outside the width and would report an unsigned hash as an
                // unreadable artifact rather than the wrong constant it is.
                string carried = written.RootElement.GetProperty("prefabs").EnumerateArray().Single()
                    .GetProperty("hash").GetRawText();
                if (carried != hash.ToString())
                {
                    failures.Add($"the hash {hash}: written as {carried}");
                }
            });
            if (thrown != null)
            {
                failures.Add($"the hash {hash}: threw {thrown.Message}, want it read");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void BuildPrefabHoldsEveryValueToTheWidthTheGameDeclaresItOver()
    {
        (string Name, Action<List<AssetTypeValueField>> Mutate, string Want)[] cases =
        {
            ("the name as a byte", fields => Replace(fields, Node("PrefabName", AssetValueType.UInt8, new AssetTypeValue((byte)2))),
                "serializes PrefabName as UInt8 rather than as String"),
            ("the hash widened", fields => Replace(fields, Node("PrefabHash", AssetValueType.Int64, new AssetTypeValue(1812372242L))),
                "serializes PrefabHash as Int64 rather than as Int32"),
            ("the draw as a byte", fields => Replace(fields, Node(Checks.PowerField, AssetValueType.UInt8, new AssetTypeValue((byte)2))),
                $"serializes {Checks.PowerField} as UInt8 rather than as Float"),
            ("a flag holding neither 0 nor 1", fields => Replace(fields, Node("HasPowerState", AssetValueType.UInt8, new AssetTypeValue((byte)2))),
                "serializes HasPowerState as the byte 2 rather than as a bool"),
            ("a slot ordinal widened", fields => Replace(fields, SlotArray(Node("Type", AssetValueType.Int32, new AssetTypeValue(2)))),
                "serializes Type as Int32 rather than as UInt16"),
        };

        Check(cases);
    }

    [Fact]
    public void BuildPrefabRefusesAFieldThatIsNotThere()
    {
        (string Name, Action<List<AssetTypeValueField>> Mutate, string Want)[] cases =
        {
            ("no name", fields => Remove(fields, "PrefabName"), "has no PrefabName field"),
            ("no hash", fields => Remove(fields, "PrefabHash"), "has no PrefabHash field"),
            ("no slot list", fields => Remove(fields, "Slots"), "has no Slots field"),
            // A dummy answers an empty child list for this, so a reader that
            // stopped requiring the array would read every prefab as declaring
            // no slots and write a roster that is full, plausible and wrong.
            ("a slot list with no array", fields => Replace(fields, Node("Slots", AssetValueType.None, null, Int("Count", 0))),
                "has no Array field"),
            ("a slot with no ordinal", fields => Replace(fields, SlotArray(Node("Class", AssetValueType.UInt16, new AssetTypeValue((ushort)2)))),
                "has no Type field"),
        };

        Check(cases);
    }

    // Thing serializes HasRunOnAtmospherics (prefix, no suffix) and DamageState
    // (suffix, no prefix), so a shape dropping either half takes one for a flag.
    // A fixture whose every field is on the safe side of the line pins neither.
    [Fact]
    public void BuildPrefabTakesAStateFlagByBothHalvesOfItsShape()
    {
        (string Name, string Field)[] neighbours =
        {
            ("the prefix without the suffix", "HasRunOnAtmospherics"),
            ("the suffix without the prefix", "DamageState"),
        };

        string[] serialized = Asset().Children.Select(field => field.FieldName).ToArray();
        List<string> failures = neighbours
            .Where(neighbour => !serialized.Contains(neighbour.Field, StringComparer.Ordinal))
            .Select(neighbour => $"{neighbour.Name}: the fixture serializes no {neighbour.Field}, so nothing holds the shape to that half")
            .ToList();

        Exception? thrown = Record.Exception(() => Build(_ => { }));
        if (thrown != null)
        {
            failures.Add($"a field off the flag shape was taken for one: {thrown.Message}");
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A subclass shadowing a base class field: Unity writes both, the asset
    // library's indexer answers the first, and this reader wants the second.
    [Fact]
    public void BuildPrefabRefusesAFieldSerializedMoreThanOnce()
    {
        (string Name, Action<List<AssetTypeValueField>> Mutate, string Want)[] cases =
        {
            ("the name twice", fields => fields.Add(Str("PrefabName", "StructureSensorMkII")),
                "serializes 2 fields named PrefabName"),
            ("the hash twice", fields => fields.Add(Int("PrefabHash", 1)),
                "serializes 2 fields named PrefabHash"),
            ("a draw shadowing the one Device declares", fields => fields.Add(Float(Checks.PowerField, 25f)),
                $"serializes 2 fields named {Checks.PowerField}"),
            ("the slot list twice", fields => fields.Add(SlotArray(Ordinal(9))),
                "serializes 2 fields named Slots"),
        };

        Check(cases);
    }

    [Fact]
    public void BuildPrefabRefusesAStateLayoutTheExtractionDoesNotModel()
    {
        (string Name, Action<List<AssetTypeValueField>> Mutate, string Want)[] cases =
        {
            ("a flag added", fields => fields.Add(Flag("HasWidgetState", true)),
                "serializes state flags this extraction does not model: added HasWidgetState"),
            ("a flag gone", fields => Remove(fields, "HasModeState"), "missing HasModeState"),
            ("a flag widened", fields => Replace(fields, Node("HasLockState", AssetValueType.Int32, new AssetTypeValue(0))),
                "serializes HasLockState as Int32 rather than as the UInt8 a bool serializes to"),
        };

        Check(cases);
    }

    // The slot class enum has 43 members today, so every ordinal the game ships
    // fits in a byte and a narrowed reader agrees on the whole current roster,
    // then names a different member on the first class past 255. The JSON is
    // read back too, being the only place the ordinal reaches a user.
    [Fact]
    public void TheWholeSlotOrdinalReachesTheArtifact()
    {
        ushort[] ordinals = { 0, 2, 5, 255, 256, 300, ushort.MaxValue };

        var failures = new List<string>();
        foreach (ushort ordinal in ordinals)
        {
            Exception? thrown = Record.Exception(() =>
            {
                Program.Prefab prefab = Build(fields => Replace(fields, SlotArray(Ordinal(ordinal))));
                ushort got = prefab.Slots.Single().Class;
                if (got != ordinal)
                {
                    failures.Add($"the ordinal {ordinal}: read as {got}");
                }

                string path = TempPath("prefabs.json");
                Program.Write(path, "0.2.5445.24403", new[] { prefab });
                using JsonDocument written = JsonDocument.Parse(File.ReadAllBytes(path));
                int carried = written.RootElement.GetProperty("prefabs").EnumerateArray().Single()
                    .GetProperty("slots").EnumerateArray().Single()
                    .GetProperty("class").GetInt32();
                if (carried != ordinal)
                {
                    failures.Add($"the ordinal {ordinal}: written as {carried}");
                }
            });
            if (thrown != null)
            {
                failures.Add($"the ordinal {ordinal}: threw {thrown.Message}, want it read");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // An index past the end of the table is the no-script sentinel, which the
    // search walks past. A table with no entries at all is not that: every asset
    // answers no script and the refusal names a class that never moved.
    [Fact]
    public void ScriptPointerTakesTheEntryTheScriptIndexNames()
    {
        var first = new AssetPPtr(0, 11);
        var last = new AssetPPtr(1, 22);
        AssetPPtr[] table = { first, last };

        var only = new AssetPPtr(0, 33);
        AssetPPtr[] single = { only };

        (string Name, AssetPPtr[] Scripts, ushort Index, AssetPPtr? Want, string? WantProblem)[] cases =
        {
            ("the first entry", table, 0, first, null),
            ("the last entry", table, 1, last, null),
            ("one past the last entry", table, 2, null, null),
            ("the no-script sentinel", table, ushort.MaxValue, null, null),
            // The smallest table a real file can carry, between the empty one
            // that is refused and the two that is read. Without it the boundary
            // moves by one and every case above still answers the same way.
            ("the one entry of a table of one", single, 0, only, null),
            ("one past the one entry of a table of one", single, 1, null, null),
            ("a table with no entries", Array.Empty<AssetPPtr>(), 0, null, "carries no script type table"),
            ("a table with no entries met through the sentinel",
                Array.Empty<AssetPPtr>(), ushort.MaxValue, null, "carries no script type table"),
        };

        var failures = new List<string>();
        foreach ((string name, var scripts, ushort index, var want, string? wantProblem) in cases)
        {
            AssetPPtr? got = null;
            Exception? thrown = Record.Exception(() => got = Program.ScriptPointer(scripts, index, "resources.assets"));
            if (wantProblem != null)
            {
                if (thrown is not RefusalException || !thrown.Message.Contains(wantProblem, StringComparison.Ordinal))
                {
                    failures.Add($"{name}: got {thrown?.Message ?? "no refusal"}, want {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want the entry");
            }
            else if (!ReferenceEquals(got, want))
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // A pointer's file id is 0 exactly when it names the file it sits in. A
    // count answering the other way takes the roster whose pointers all leave
    // for the self-contained one, and reads a wrong roster out of sharedassets.
    [Fact]
    public void ExternalEntriesCountsThePointersThatLeaveTheFile()
    {
        (string Name, int[] FileIds, int Want)[] cases =
        {
            ("a roster with no entries", Array.Empty<int>(), 0),
            ("every pointer inside the file", new[] { 0, 0, 0 }, 0),
            ("one pointer out of three leaving", new[] { 0, 2, 0 }, 1),
            ("every pointer leaving", new[] { 1, 2, 3 }, 3),
            ("a single pointer inside the file", new[] { 0 }, 0),
            ("a single pointer leaving", new[] { 1 }, 1),
        };

        var failures = new List<string>();
        foreach ((string name, int[] fileIds, int want) in cases)
        {
            int got = Program.ExternalEntries(Pointers(fileIds), "the WorldManager at pathId 9");
            if (got != want)
            {
                failures.Add($"{name}: counted {got}, want {want}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The build ships the class on more than one asset and only some carry
    // SourcePrefabs, so refusing here refuses every real depot. Only ChooseRoster
    // can tell a field carried nowhere from a renamed class.
    [Fact]
    public void CandidateWalksPastAWorldManagerCarryingNoRoster()
    {
        AssetTypeValueField entries = Pointers(0, 4, 0);
        AssetTypeValueField carrying = Node("Base", AssetValueType.None, null,
            Node("SourcePrefabs", AssetValueType.None, null, entries));

        (AssetTypeValueField Entries, int External, int Total)? got = Program.Candidate(carrying, "the WorldManager at pathId 9");
        Assert.NotNull(got);
        Assert.Same(entries, got.Value.Entries);
        Assert.Equal(1, got.Value.External);
        Assert.Equal(3, got.Value.Total);

        AssetTypeValueField bare = Node("Base", AssetValueType.None, null, Int("Version", 1));
        Assert.Null(Program.Candidate(bare, "the WorldManager at pathId 7"));
    }

    // A WorldManager carrying no roster is walked past; one whose roster is not
    // the shape this reader reads is the layout having moved.
    [Fact]
    public void CandidateRefusesARosterItCannotRead()
    {
        (string Name, AssetTypeValueField WorldManager, string Want)[] cases =
        {
            ("a roster with no array",
                Node("Base", AssetValueType.None, null, Node("SourcePrefabs", AssetValueType.None, null, Int("Count", 0))),
                "has no Array field"),
            ("the roster serialized twice",
                Node("Base", AssetValueType.None, null,
                    Node("SourcePrefabs", AssetValueType.None, null, Pointers(0)),
                    Node("SourcePrefabs", AssetValueType.None, null, Pointers(0))),
                "serializes 2 fields named SourcePrefabs"),
        };

        var failures = new List<string>();
        foreach ((string name, var worldManager, string want) in cases)
        {
            Exception? thrown = Record.Exception(() => Program.Candidate(worldManager, "the WorldManager at pathId 9"));
            if (thrown is not RefusalException || !thrown.Message.Contains(want, StringComparison.Ordinal))
            {
                failures.Add($"{name}: got {thrown?.Message ?? "no refusal"}, want {want}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The entry is named rather than the roster, because a roster of 1565
    // pointers with one unreadable is otherwise a diagnostic nobody can act on.
    [Fact]
    public void ExternalEntriesRefusesAPointerItCannotRead()
    {
        AssetTypeValueField widened = Node("Array", AssetValueType.Array, null,
            Node("data", AssetValueType.None, null, Int("m_FileID", 0)),
            Node("data", AssetValueType.None, null, Node("m_FileID", AssetValueType.Int64, new AssetTypeValue(0L))));

        string message = Assert.IsType<RefusalException>(
            Record.Exception(() => Program.ExternalEntries(widened, "the WorldManager at pathId 9"))).Message;
        Assert.Contains("entry 1 of the WorldManager at pathId 9", message, StringComparison.Ordinal);
        Assert.Contains("serializes m_FileID as Int64 rather than as Int32", message, StringComparison.Ordinal);
    }

    // The game declares several distinct classes under one bare name, and the
    // decompiled tree tools/isagen joins against is laid out by the qualified
    // one. An empty namespace is the global one and is an answer; an absent one
    // is a field not found, and the two produce the same bare name.
    [Fact]
    public void ScriptClassNameQualifiesTheClass()
    {
        (string Name, AssetTypeValueField Script, string? Want, string? WantProblem)[] cases =
        {
            ("a class in a namespace",
                Node("Base", AssetValueType.None, null,
                    Str("m_ClassName", "Device"), Str("m_Namespace", "Assets.Scripts.Objects.Pipes")),
                "Assets.Scripts.Objects.Pipes.Device", null),
            ("a class in the global namespace",
                Node("Base", AssetValueType.None, null, Str("m_ClassName", "WorldManager"), Str("m_Namespace", "")),
                "WorldManager", null),
            ("a script naming no class, in a namespace",
                Node("Base", AssetValueType.None, null,
                    Str("m_ClassName", ""), Str("m_Namespace", "Assets.Scripts.Objects.Pipes")),
                null, null),
            ("a script naming no class, in the global namespace",
                Node("Base", AssetValueType.None, null, Str("m_ClassName", ""), Str("m_Namespace", "")), null, null),
            ("a script with no class name",
                Node("Base", AssetValueType.None, null, Str("m_Namespace", "")), null, "has no m_ClassName field"),
            ("a script with no namespace",
                Node("Base", AssetValueType.None, null, Str("m_ClassName", "Device")), null, "has no m_Namespace field"),
        };

        var failures = new List<string>();
        foreach ((string name, var script, string? want, string? wantProblem) in cases)
        {
            string? got = null;
            Exception? thrown = Record.Exception(() => got = Program.ScriptClassName(script, "the script"));
            if (wantProblem != null)
            {
                if (thrown is not RefusalException || !thrown.Message.Contains(wantProblem, StringComparison.Ordinal))
                {
                    failures.Add($"{name}: got {thrown?.Message ?? Show(got)}, want {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want {Show(want)}");
            }
            else if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The refusal names a script rather than the prefab driven by it, the prefab
    // not being known at this point in the walk. The sentinel is answered
    // without following anything, because the search meets it on most assets and
    // a read there is a deserialization per asset of a several-hundred-MB file.
    [Fact]
    public void ScriptClassFollowsTheScriptTableEntryToTheClassItNames()
    {
        AssetPPtr[] table = { new(0, 11), new(1, 22) };

        AssetTypeValueField Script(string ns, string name) =>
            Node("Base", AssetValueType.None, null, Str("m_ClassName", name), Str("m_Namespace", ns));

        (string Name, AssetPPtr[] Scripts, ushort Index, Func<AssetPPtr, AssetTypeValueField?>? Read, string? Want, string? WantProblem)[] cases =
        {
            ("the class the entry names", table, 0, _ => Script("Assets.Scripts.Objects.Pipes", "Device"),
                "Assets.Scripts.Objects.Pipes.Device", null),
            // The pointer the read is handed is the entry the index picks rather
            // than the first of the table.
            ("the entry the index picks", table, 1, pptr => Script("", $"Class{pptr.FileId}_{pptr.PathId}"),
                "Class1_22", null),
            ("an index the table has no entry for", table, 2, null, null, null),
            ("the no-script sentinel", table, ushort.MaxValue, null, null, null),
            ("a script that does not deserialize", table, 1, _ => null, null, "script 1:22 does not deserialize"),
            ("a script naming no class", table, 0, _ => Script("Assets.Scripts.Objects.Pipes", ""), null, null),
            ("a script this reader cannot read a namespace off", table, 0,
                _ => Node("Base", AssetValueType.None, null, Str("m_ClassName", "Device")), null,
                "the script at 0:11 has no m_Namespace field"),
            ("a file carrying no script type table", Array.Empty<AssetPPtr>(), 0, null, null,
                "resources.assets carries no script type table, so nothing in it names the class driving it"),
        };

        var failures = new List<string>();
        foreach ((string name, var scripts, ushort index, var read, string? want, string? wantProblem) in cases)
        {
            int reads = 0;
            string? got = null;
            Exception? thrown = Record.Exception(() => got = Program.ScriptClass(
                scripts, index, "resources.assets", pptr => { reads++; return read!(pptr); }));

            if (wantProblem != null)
            {
                if (thrown is not RefusalException || thrown.Message != wantProblem)
                {
                    failures.Add($"{name}: got {thrown?.Message ?? Show(got)}, want {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want {Show(want)}");
            }
            else if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }

            if (read == null && reads > 0)
            {
                failures.Add($"{name}: followed {reads} script pointers, want none followed at all");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The failure types are ones those library calls were seen to raise against
    // real bad inputs, and are not one family: a wrong format raises
    // NotSupportedException and an unreadable file UnauthorizedAccessException,
    // neither of which is an IOException.
    [Fact]
    public void InputProblemSpellsABuildInputItCouldNotOpen()
    {
        string present = TempPath(Checks.ResourcesFile);
        File.WriteAllBytes(present, new byte[] { 0 });
        string absent = TempPath(Checks.ResourcesFile);
        // GetDirectoryName is null only for a root path, which this is not.
        string holding = Path.GetDirectoryName(present)!;

        string Unreadable(Exception thrown) =>
            $"{present}: this {Checks.ResourcesFile} does not read, so there is no prefab roster to read: {thrown.Message}";

        var truncated = new EndOfStreamException("Unable to read beyond the end of the stream.");
        var wrongFormat = new NotSupportedException("TPK* magic not found. Is this really a tpk file?");
        var denied = new UnauthorizedAccessException($"Access to the path '{present}' is denied.");
        var device = new IOException("Input/output error.");
        var tooBig = new OutOfMemoryException();
        var unanticipated = new NullReferenceException();

        (string Name, string Path, Exception? Thrown, string? Want, bool Escapes)[] cases =
        {
            ("a file that opens", present, null, null, false),
            ("a file that is not there", absent, null,
                $"{absent}: no {Checks.ResourcesFile} here, so there is no prefab roster to read", false),
            // A directory under that name is the same absence, not a file this
            // reader could go on to open.
            ("a directory where the file should be", holding, null,
                $"{holding}: no {Checks.ResourcesFile} here, so there is no prefab roster to read", false),
            ("a file that ends before the reader is done with it", present, truncated, Unreadable(truncated), false),
            ("a file the read failed on some other way", present, device, Unreadable(device), false),
            ("a file that is not of that format at all", present, wrongFormat, Unreadable(wrongFormat), false),
            ("a file the process may not open", present, denied, Unreadable(denied), false),
            ("a machine too small for the file", present, tooBig, null, true),
            ("a failure this reader never anticipated", present, unanticipated, null, true),
        };

        var failures = new List<string>();
        foreach ((string name, string path, var thrown, string? want, bool escapes) in cases)
        {
            string? got = null;
            object? opened = null;
            Exception? raised = Record.Exception(() => got = Program.InputProblem(
                path,
                Checks.ResourcesFile,
                "so there is no prefab roster to read",
                _ => thrown == null ? new object() : throw thrown,
                out opened));

            if (escapes)
            {
                if (!ReferenceEquals(raised, thrown))
                {
                    failures.Add($"{name}: got {raised?.ToString() ?? "no failure"}, want the failure itself");
                }
                continue;
            }

            if (raised != null)
            {
                failures.Add($"{name}: threw {raised.Message}, want {Show(want)}");
            }
            else if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
            else if (want == null ? opened == null : opened != null)
            {
                failures.Add($"{name}: answered {(opened == null ? "nothing" : "an input")} beside {Show(got)}");
            }
        }

        Exception? nothing = Record.Exception(() => Program.InputProblem(
            present, Checks.ResourcesFile, "so there is no prefab roster to read", _ => (object)null!, out _));
        string wantNothing =
            $"{present}: opening this {Checks.ResourcesFile} answered nothing and raised nothing, which is neither thing it does";
        if (nothing is not InvalidOperationException || nothing.Message != wantNothing)
        {
            failures.Add($"an open that answered nothing: got {nothing?.ToString() ?? "no failure"}, want {wantNothing}");
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The narrow shape, carrying an honest header and however many bytes the
    // case wants behind it.
    private static byte[] Serialized(uint fileSize, uint dataOffset, int length)
    {
        var bytes = new byte[length];
        void Be32(int at, uint value)
        {
            for (int i = 0; i < 4; i++)
            {
                bytes[at + i] = (byte)(value >> ((3 - i) * 8));
            }
        }
        Be32(0, 574);
        Be32(4, fileSize);
        Be32(8, 21);
        Be32(12, dataOffset);
        return bytes;
    }

    // The asset reader opens a truncated serialized file without complaining, so
    // a gate behind it never runs and one beside it runs too late. Every row
    // also asserts whether the library was handed the file, because a gate that
    // refuses and hands it over has settled a layout off bytes it just refused.
    [Fact]
    public void ResourcesProblemHoldsTheFileToItsOwnHeaderBeforeTheLibrarySeesIt()
    {
        string directory = TempDirectory();

        string Put(string name, byte[] bytes)
        {
            string path = Path.Combine(directory, name);
            File.WriteAllBytes(path, bytes);
            return path;
        }

        string whole = Put("whole.assets", Serialized(4096, 1024, 4096));
        string truncated = Put("truncated.assets", Serialized(4096, 1024, 2048));
        string tiny = Put("tiny.assets", new byte[] { 0, 1, 2 });
        string absent = Path.Combine(directory, "gone.assets");
        var refused = new NotSupportedException("Cannot read this file.");

        (string Name, string Path, Exception? Thrown, string? Want, bool WantOpened)[] cases =
        {
            ("a file whose header is the file", whole, null, null, true),
            ("a download that stopped part way", truncated, null,
                $"{truncated}: this file declares itself 4096 bytes and is 2048, so there is no prefab roster to read", false),
            ("a file with no header in it at all", tiny, null,
                $"{tiny}: 3 bytes, which is not even a serialized file header, so there is no prefab roster to read", false),
            ("a file that is not there", absent, null,
                $"{absent}: no serialized file here, so there is no prefab roster to read", false),
            ("a directory where the file should be", directory, null,
                $"{directory}: no serialized file here, so there is no prefab roster to read", false),
            ("a file the header cannot fault and the library will not read", whole, refused,
                $"{whole}: this serialized file does not read, so there is no prefab roster to read: {refused.Message}", true),
        };

        var failures = new List<string>();
        foreach ((string name, string path, var thrown, string? want, bool wantOpened) in cases)
        {
            bool opened = false;
            string? got = Program.ResourcesProblem(
                path,
                _ =>
                {
                    opened = true;
                    return thrown == null ? new AssetsFileInstance(new AssetsFile(), path) : throw thrown;
                },
                _ => Array.Empty<string>(),
                out AssetsFileInstance? answer);

            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
            else if (opened != wantOpened)
            {
                failures.Add($"{name}: {(opened ? "handed" : "did not hand")} the file to the library, want the other");
            }
            else if (want == null ? answer == null : answer != null)
            {
                failures.Add($"{name}: answered {(answer == null ? "nothing" : "a file")} beside {Show(got)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The library follows every file the roster's own declares and follows them
    // again from each it reaches. In the build this was written against, 771 of
    // the 773 entries in the script type table point into
    // globalgamemanagers.assets, so a truncated copy of that reads as a rename.
    [Fact]
    public void ResourcesProblemHoldsEveryFileTheWalkReadsThrough()
    {
        string directory = TempDirectory();
        Directory.CreateDirectory(Path.Combine(directory, "Resources"));

        string Put(string name, byte[] bytes)
        {
            string path = Path.Combine(directory, name);
            File.WriteAllBytes(path, bytes);
            return path;
        }

        string roster = Put(Checks.ResourcesFile, Serialized(4096, 1024, 4096));
        string managers = Put("globalgamemanagers.assets", Serialized(2048, 512, 2048));
        string shared = Put("sharedassets0.assets", Serialized(1024, 256, 1024));
        string builtin = Put("Resources/unity_builtin_extra", Serialized(512, 128, 512));
        string shortManagers = Put("short.assets", Serialized(2048, 512, 1024));
        string headless = Put("headless.assets", new byte[] { 0, 1, 2 });
        string cased = Put("Extra.assets", Serialized(768, 192, 768));
        Put("extra.assets", Serialized(2048, 512, 1024));

        const string notAllThere = "so the roster would be read through a file that is not all there";
        string Truncated(string path, long declares, long length) =>
            $"{path}: this file declares itself {declares} bytes and is {length}, {notAllThere}";

        (string Name, Dictionary<string, string[]> Declares, string? Want, string[] WantOpened)[] cases =
        {
            ("a file declaring nothing", Declaring(), null, new[] { roster }),
            ("every file it declares whole",
                Declaring((roster, new[] { "globalgamemanagers.assets", "Resources/unity_builtin_extra" })),
                null, new[] { roster, managers, builtin }),
            ("a declared file the depot did not fetch",
                Declaring((roster, new[] { "Resources/unity default resources" })), null, new[] { roster }),
            ("a declared file with no path at all",
                Declaring((roster, new[] { string.Empty })), null, new[] { roster }),
            ("a declared file the fetch stopped part way through",
                Declaring((roster, new[] { "short.assets" })), Truncated(shortManagers, 2048, 1024), new[] { roster }),
            ("a declared file with no header in it at all",
                Declaring((roster, new[] { "headless.assets" })),
                $"{headless}: 3 bytes, which is not even a serialized file header, {notAllThere}", new[] { roster }),
            ("two declared files wrong at once",
                Declaring((roster, new[] { "short.assets", "headless.assets" })),
                Truncated(shortManagers, 2048, 1024) + "; " +
                    $"{headless}: 3 bytes, which is not even a serialized file header, {notAllThere}",
                new[] { roster }),
            ("a file reached only through another one",
                Declaring(
                    (roster, new[] { "globalgamemanagers.assets" }),
                    (managers, new[] { "short.assets" })),
                Truncated(shortManagers, 2048, 1024), new[] { roster, managers }),
            ("a declared file declaring the one that declared it",
                Declaring(
                    (roster, new[] { "globalgamemanagers.assets" }),
                    (managers, new[] { Checks.ResourcesFile })),
                null, new[] { roster, managers }),
            ("one file declared by two",
                Declaring(
                    (roster, new[] { "globalgamemanagers.assets", "sharedassets0.assets" }),
                    (managers, new[] { "sharedassets0.assets" })),
                null, new[] { roster, managers, shared }),
            // The library looks both ways round, so a gate looking only the
            // first way walks past a file the library then opens.
            ("a declared file found under the last part of its name",
                Declaring((roster, new[] { "Library/sharedassets0.assets" })), null, new[] { roster, shared }),
            // The library holds what it opened under the last part of the path
            // lowered, so these are one file to it. This walk remembers by that
            // same name and never opens the second, which the library would
            // never open either.
            ("two declared files whose names differ only in case",
                Declaring((roster, new[] { "Extra.assets", "extra.assets" })), null, new[] { roster, cased }),
        };

        var failures = new List<string>();
        foreach ((string name, var declares, string? want, string[] wantOpened) in cases)
        {
            var opened = new List<string>();
            string? got = Program.ResourcesProblem(
                roster,
                path =>
                {
                    opened.Add(path);
                    return new AssetsFileInstance(new AssetsFile(), path);
                },
                file => declares.TryGetValue(file.path, out string[]? declared) ? declared : Array.Empty<string>(),
                out AssetsFileInstance? answer);

            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
            else if (want == null ? answer == null : answer != null)
            {
                failures.Add($"{name}: answered {(answer == null ? "nothing" : "a file")} beside {Show(got)}");
            }

            if (!opened.OrderBy(p => p, StringComparer.Ordinal)
                .SequenceEqual(wantOpened.OrderBy(p => p, StringComparer.Ordinal), StringComparer.Ordinal))
            {
                failures.Add($"{name}: opened {string.Join(", ", opened)}, want {string.Join(", ", wantOpened)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    private static Dictionary<string, string[]> Declaring(params (string Path, string[] Declared)[] files) =>
        files.ToDictionary(file => file.Path, file => file.Declared, StringComparer.Ordinal);

    // The asset library reads a package that is not all there without
    // complaining and answers with every engine version the whole one covers, so
    // the two checks behind this pass on a database built out of zeros. A file
    // with no magic is left to the library, whose sentence names what it wanted.
    [Fact]
    public void PackageProblemHoldsThePackageToItsOwnHeaderBeforeTheLibrarySeesIt()
    {
        string directory = TempDirectory();

        string Put(string name, byte[] bytes)
        {
            string path = Path.Combine(directory, name);
            File.WriteAllBytes(path, bytes);
            return path;
        }

        string whole = Put("classdata.tpk", Package(1, 4076, 4096));
        string truncated = Put("truncated.tpk", Package(1, 4076, 2048));
        string newer = Put("newer.tpk", Package(2, 4076, 4096));
        string page = Put("page.tpk", Encoding.UTF8.GetBytes("<!DOCTYPE html><html><body>404</body></html>"));
        string absent = Path.Combine(directory, "gone.tpk");
        var refused = new NotSupportedException("TPK* magic not found. Is this really a tpk file?");

        const string noTypes = "so nothing here describes the engine types the serialized files were written by";

        (string Name, string Path, Exception? Thrown, string? Want, bool WantOpened)[] cases =
        {
            ("a package whose header is the file", whole, null, null, true),
            ("a fetch that stopped part way", truncated, null,
                $"{truncated}: this file declares 4076 bytes of class data behind its 20-byte header and is 2048, {noTypes}",
                false),
            ("a package written in a version newer than the reader reads", newer, null,
                $"{newer}: this file is written in class package format version 2, and this reader reads up to 1, {noTypes}",
                false),
            ("a page a failed fetch saved under this name", page, refused,
                $"{page}: this class package does not read, {noTypes}: {refused.Message}", true),
            ("a package that is not there", absent, null, $"{absent}: no class package here, {noTypes}", false),
            ("a directory where the package should be", directory, null,
                $"{directory}: no class package here, {noTypes}", false),
        };

        var failures = new List<string>();
        foreach ((string name, string path, var thrown, string? want, bool wantOpened) in cases)
        {
            bool opened = false;
            string? got = Program.PackageProblem(
                path,
                _ =>
                {
                    opened = true;
                    return thrown == null ? new ClassPackageFile() : throw thrown;
                },
                out ClassPackageFile? answer);

            if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
            else if (opened != wantOpened)
            {
                failures.Add($"{name}: {(opened ? "handed" : "did not hand")} the file to the library, want the other");
            }
            else if (want == null ? answer == null : answer != null)
            {
                failures.Add($"{name}: answered {(answer == null ? "nothing" : "a package")} beside {Show(got)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The header the asset library writes: magic, format version, compression
    // scheme and data type, a reserved byte and word, then the compressed and
    // decompressed sizes. Every number is least significant byte first.
    private static byte[] Package(byte version, uint compressedSize, int length)
    {
        var bytes = new byte[length];
        Encoding.UTF8.GetBytes("TPK*").CopyTo(bytes, 0);
        bytes[4] = version;
        bytes[5] = 1;
        void Le32(int at, uint value)
        {
            for (int i = 0; i < 4; i++)
            {
                bytes[at + i] = (byte)(value >> (i * 8));
            }
        }
        Le32(12, compressedSize);
        Le32(16, compressedSize * 4);
        return bytes;
    }

    // Every number is read least significant byte first, the other way round
    // from the serialized file header: a reader taking them that way reads a
    // version of 1 as 16777216 and refuses the pinned build input.
    [Fact]
    public void PackageExtentReadsWhatTheClassPackageHeaderClaims()
    {
        byte[] pinned = Package(1, 289585, 20);
        byte[] newer = Package(2, 289585, 20);
        byte[] widest = Package(255, 0xFFFFFFFF, 20);
        byte[] wrongMagic = Package(1, 289585, 20);
        wrongMagic[3] = (byte)'!';

        (string Name, byte[] Bytes, int Take, int Chunk, Checks.PackageExtent? Want)[] cases =
        {
            ("the header the build pins", pinned, 20, 0, new Checks.PackageExtent(1, 289585)),
            ("a header naming a newer format version", newer, 20, 0, new Checks.PackageExtent(2, 289585)),
            ("every bit of both numbers set", widest, 20, 0, new Checks.PackageExtent(255, 4294967295)),
            ("a file carrying some other magic", wrongMagic, 20, 0, null),
            ("one byte short of the header", pinned, 19, 0, null),
            ("the magic and nothing behind it", pinned, 4, 0, null),
            ("nothing at all", pinned, 0, 0, null),
            ("a stream handing over one byte at a time", pinned, 20, 1, new Checks.PackageExtent(1, 289585)),
            ("a stream handing over seven bytes at a time", pinned, 20, 7, new Checks.PackageExtent(1, 289585)),
        };

        var failures = new List<string>();
        foreach ((string name, byte[] bytes, int take, int chunk, var want) in cases)
        {
            Stream stream = new MemoryStream(bytes, 0, take);
            if (chunk > 0)
            {
                stream = new Dribbling(stream, chunk);
            }
            Checks.PackageExtent? got = Program.PackageExtent(stream);
            if (got != want)
            {
                failures.Add($"{name}: read {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Every number is read most significant byte first, and the format version
    // decides which pair is the file's own. A reader taking the 32-bit pair off
    // a file of the wider shape takes two legacy fields the writer is free to
    // leave at anything, and holds the file to a claim it never made.
    [Fact]
    public void ExtentReadsWhatTheHeaderClaimsAtEitherWidth()
    {
        static byte[] Header(uint version, uint fileSize, uint dataOffset, long wideSize, long wideOffset)
        {
            var bytes = new byte[48];
            void Be32(int at, uint value)
            {
                for (int i = 0; i < 4; i++)
                {
                    bytes[at + i] = (byte)(value >> ((3 - i) * 8));
                }
            }
            void Be64(int at, long value)
            {
                for (int i = 0; i < 8; i++)
                {
                    bytes[at + i] = (byte)(value >> ((7 - i) * 8));
                }
            }
            Be32(0, 574);
            Be32(4, fileSize);
            Be32(8, version);
            Be32(12, dataOffset);
            Be64(24, wideSize);
            Be64(32, wideOffset);
            return bytes;
        }

        byte[] narrow = Header(21, 23232, 4096, 0x0BADF00DBADF00D, 0x0BADF00DBADF00D);
        byte[] wide = Header(22, 0xFFFFFFFF, 0xFFFFFFFF, 734003200, 262144);

        (string Name, byte[] Bytes, int Take, int Chunk, Checks.SerializedExtent? Want)[] cases =
        {
            ("a file of the shape that holds its size in 32 bits", narrow, 48, 0,
                new Checks.SerializedExtent(23232, 4096)),
            ("that shape with nothing behind its own header", narrow, 20, 0,
                new Checks.SerializedExtent(23232, 4096)),
            ("one byte short of that header", narrow, 19, 0, null),
            ("nothing at all", narrow, 0, 0, null),
            ("a file of the shape that holds its size in 64 bits", wide, 48, 0,
                new Checks.SerializedExtent(734003200, 262144)),
            ("that shape with only the narrow header present", wide, 20, 0, null),
            ("one byte short of the wider header", wide, 47, 0, null),
            ("a stream handing over one byte at a time", wide, 48, 1,
                new Checks.SerializedExtent(734003200, 262144)),
            ("a stream handing over seven bytes at a time", narrow, 48, 7,
                new Checks.SerializedExtent(23232, 4096)),
        };

        var failures = new List<string>();
        foreach ((string name, byte[] bytes, int take, int chunk, var want) in cases)
        {
            Stream stream = new MemoryStream(bytes, 0, take);
            if (chunk > 0)
            {
                stream = new Dribbling(stream, chunk);
            }
            Checks.SerializedExtent? got = Program.SerializedExtent(stream);
            if (got != want)
            {
                failures.Add($"{name}: read {Show(got)}, want {Show(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Dribbling hands over at most a few bytes per read, which every stream is
    // allowed to do and a file on a slow device does. A header reader taking the
    // first answer for the whole reads the rest out of bytes nothing wrote.
    private sealed class Dribbling : Stream
    {
        private readonly Stream inner;
        private readonly int most;

        internal Dribbling(Stream inner, int most)
        {
            this.inner = inner;
            this.most = most;
        }

        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => inner.Length;
        public override long Position { get => inner.Position; set => throw new NotSupportedException(); }
        public override int Read(byte[] buffer, int offset, int count) =>
            inner.Read(buffer, offset, Math.Min(count, most));
        public override void Flush() => inner.Flush();
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
        public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
    }

    // The package is a pinned build input this suite ships no copy of and the
    // database is built inside the asset library, so both are parameters. Only
    // these rows reach the conversion from the library's spelling into something
    // orderable; the comparison itself is exercised on hand-built versions.
    [Fact]
    public void ClassDatabaseProblemHoldsThePackageAndTheDatabaseItBuilt()
    {
        UnityVersion[] covered = { new("2021.3.14f1"), new("2022.3.41f1"), new("2022.1.9f1") };

        const string leftOut = ", so every engine type that moved since would be left out of the database rather than reported";
        const string noLayout = ", so the assets the roster is read out of have no layout";
        const string notAVersion = ", which is not an engine version this reader can order a class package against";

        (string Name, string? Wanted, UnityVersion[] Covered, IReadOnlyList<string> Unresolved, string? Want)[] cases =
        {
            ("a package covering the build, every class described",
                "2022.3.41f1", covered, Array.Empty<string>(), null),
            ("a package one patch older than the build",
                "2022.3.42f1", covered, Array.Empty<string>(),
                "the serialized files were written by Unity 2022.3.42f1, newer than the Unity 2022.3.41f1 the class package describes" + leftOut),
            ("a package a minor release older than the build",
                "2022.4.5f1", covered, Array.Empty<string>(),
                "the serialized files were written by Unity 2022.4.5f1, newer than the Unity 2022.3.41f1 the class package describes" + leftOut),
            ("an older minor release, patched past the newest described",
                "2022.2.99f1", covered, Array.Empty<string>(), null),
            ("a package a major release older than the build",
                "2023.1.0f1", covered, Array.Empty<string>(),
                "the serialized files were written by Unity 2023.1.0f1, newer than the Unity 2022.3.41f1 the class package describes" + leftOut),
            ("a package describing no version at all",
                "2022.3.41f1", Array.Empty<UnityVersion>(), Array.Empty<string>(),
                "the class package describes no engine version at all, so it cannot describe the engine the serialized files name, Unity 2022.3.41f1"),
            ("a database describing no MonoBehaviour",
                "2022.3.41f1", covered, new[] { "MonoBehaviour" },
                "the class database built for Unity 2022.3.41f1 describes no MonoBehaviour" + noLayout),
            ("a database describing neither engine class",
                "2022.3.41f1", covered, new[] { "MonoScript", "MonoBehaviour" },
                "the class database built for Unity 2022.3.41f1 describes no MonoBehaviour, MonoScript" + noLayout),
            ("serialized files naming a zero version",
                "0.0.0", covered, Array.Empty<string>(),
                "the serialized files name Unity 0.0.0" + notAVersion),
            // The header shapes a serialized file actually carries when it names
            // no engine, none of them the literal above. The library's version
            // constructor dereferences what it is handed and refuses text it
            // cannot split, so both arrive as a throw rather than a version.
            ("a header the reader of the serialized files left unset",
                null, covered, Array.Empty<string>(),
                "the serialized files name no engine at all" + notAVersion),
            ("a header carrying nothing",
                "", covered, Array.Empty<string>(),
                "the serialized files name the text \"\"" + notAVersion),
            ("a header carrying text that is not a version",
                "steamdepot", covered, Array.Empty<string>(),
                "the serialized files name the text \"steamdepot\"" + notAVersion),
            // Wider than the int the library keeps each version part in, which
            // fails differently from text it cannot split at all.
            ("a header carrying a number no version part holds",
                "99999999999.1.1", covered, Array.Empty<string>(),
                "the serialized files name the text \"99999999999.1.1\"" + notAVersion),
            // A header that is not a version arrives on the same build as a
            // package covering none, so the two are asked together.
            ("neither the files nor the package naming an engine",
                null, Array.Empty<UnityVersion>(), Array.Empty<string>(),
                "the serialized files name no engine at all" + notAVersion +
                "; the class package describes no engine version at all, " +
                "so it cannot describe the engine the serialized files name, no engine at all"),
        };

        var failures = new List<string>();
        foreach ((string name, string? wanted, var packaged, var unresolved, string? want) in cases)
        {
            string? asked = null;
            string? got = null;
            Exception? thrown = Record.Exception(() => got = Program.ClassDatabaseProblem(wanted, packaged, version =>
            {
                asked = version;
                return unresolved;
            }));

            if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want {Show(want)}");
            }
            else if (got != want)
            {
                failures.Add($"{name}: got {Show(got)}, want {Show(want)}");
            }
            else if (asked != null && asked != wanted)
            {
                failures.Add($"{name}: the database was built for {asked}, want {wanted}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // MonoBehaviour is every asset the walk reads and MonoScript names the class
    // driving one. The database describes each independently, so one built for
    // the wrong engine can be missing one and not the other.
    [Fact]
    public void UnresolvedNamesTheEngineClassesWithNoLayout()
    {
        (string Name, AssetClassID[] Described, string[] Want)[] cases =
        {
            ("a database describing both", new[] { AssetClassID.MonoBehaviour, AssetClassID.MonoScript }, Array.Empty<string>()),
            ("a database describing no MonoBehaviour", new[] { AssetClassID.MonoScript }, new[] { "MonoBehaviour" }),
            ("a database describing no MonoScript", new[] { AssetClassID.MonoBehaviour }, new[] { "MonoScript" }),
            ("a database describing neither", Array.Empty<AssetClassID>(), new[] { "MonoBehaviour", "MonoScript" }),
            ("a database describing an unrelated class", new[] { AssetClassID.GameObject },
                new[] { "MonoBehaviour", "MonoScript" }),
        };

        var failures = new List<string>();
        foreach ((string name, var described, string[] want) in cases)
        {
            IReadOnlyList<string> got = Program.Unresolved(id => described.Contains(id));
            if (!got.SequenceEqual(want, StringComparer.Ordinal))
            {
                failures.Add($"{name}: got {Checks.JoinNames(got)}, want {Checks.JoinNames(want)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The library builds a database for any version and fills its class list by
    // asking every class it knows for the layout it had then, so a package with
    // nothing to say leaves that list empty rather than answering no database.
    [Fact]
    public void UnresolvedEngineClassesReadsWhatTheDatabaseCameOutDescribing()
    {
        ClassDatabaseFile Describing(params AssetClassID[] classes) =>
            new() { Classes = classes.Select(id => new ClassDatabaseType { ClassId = (int)id }).ToList() };

        (string Name, ClassDatabaseFile Database, string[] Want)[] cases =
        {
            ("a database describing both engine classes",
                Describing(AssetClassID.MonoBehaviour, AssetClassID.MonoScript), Array.Empty<string>()),
            ("a database describing no MonoScript", Describing(AssetClassID.MonoBehaviour), new[] { "MonoScript" }),
            ("a database describing no MonoBehaviour", Describing(AssetClassID.MonoScript), new[] { "MonoBehaviour" }),
            ("a database describing no class at all", Describing(), new[] { "MonoBehaviour", "MonoScript" }),
            ("a database describing an unrelated class", Describing(AssetClassID.GameObject),
                new[] { "MonoBehaviour", "MonoScript" }),
        };

        var failures = new List<string>();
        foreach ((string name, var database, string[] want) in cases)
        {
            string? asked = null;
            IReadOnlyList<string> got = Program.UnresolvedEngineClasses(
                version =>
                {
                    asked = version;
                    return database;
                },
                "2022.3.41f1");

            if (!got.SequenceEqual(want, StringComparer.Ordinal))
            {
                failures.Add($"{name}: named {Checks.JoinNames(got)}, want {Checks.JoinNames(want)}");
            }

            if (asked != "2022.3.41f1")
            {
                failures.Add($"{name}: the database was built for {Show(asked)}, want \"2022.3.41f1\"");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // An asset is deserialized only where its class matched: the file holds
    // several thousand MonoBehaviours and reading each is a walk this reader
    // would not finish, so every row says which assets were read.
    [Fact]
    public void SourcePrefabsTakesTheRosterOffTheWorldManagersItFound()
    {
        AssetTypeValueField Carrying(AssetTypeValueField entries) =>
            Node("Base", AssetValueType.None, null, Node("SourcePrefabs", AssetValueType.None, null, entries));

        AssetTypeValueField roster = Pointers(new int[1565]);
        AssetTypeValueField reaching = Pointers(Enumerable.Range(0, 1565).Select(index => index == 3 ? 2 : 0).ToArray());
        AssetTypeValueField small = Pointers(new int[999]);

        (string Name, (long PathId, string Script, AssetTypeValueField? Deserialized)[] Assets, long Want, string? WantProblem, Type? WantType, long[] WantRead)[] cases =
        {
            ("the one self-contained roster of two",
                new[]
                {
                    (5L, "Assets.Scripts.Objects.Thing", (AssetTypeValueField?)null),
                    (7L, Checks.WorldManagerClass, (AssetTypeValueField?)Carrying(reaching)),
                    (9L, Checks.WorldManagerClass, Carrying(roster)),
                },
                9L, null, null, new[] { 7L, 9L }),
            ("a WorldManager the read answered nothing for",
                new[] { (7L, Checks.WorldManagerClass, (AssetTypeValueField?)null) },
                0L,
                $"deserializing the {Checks.WorldManagerClass} at pathId 7 answered nothing and raised nothing, " +
                "which is neither thing it does",
                typeof(InvalidOperationException), new[] { 7L }),
            ("no asset of the class at all",
                new[] { (5L, "Assets.Scripts.Objects.Thing", (AssetTypeValueField?)null) },
                0L,
                $"resources.assets holds no {Checks.WorldManagerClass} at all, " +
                "so the class the prefab roster hangs off was renamed or moved",
                typeof(RefusalException), Array.Empty<long>()),
            // Walked past but counted all the same, the count being the whole of
            // what separates this refusal from the one above.
            ("every WorldManager carrying no roster",
                new[]
                {
                    (7L, Checks.WorldManagerClass, (AssetTypeValueField?)Node("Base", AssetValueType.None, null, Int("Version", 1))),
                    (9L, Checks.WorldManagerClass, Node("Base", AssetValueType.None, null, Int("Version", 1))),
                },
                0L,
                $"none of the 2 {Checks.WorldManagerClass} in resources.assets carries {Checks.RosterField}, " +
                "so the field the prefab roster is named by was renamed or moved",
                typeof(RefusalException), new[] { 7L, 9L }),
            ("a self-contained roster below the floor",
                new[] { (9L, Checks.WorldManagerClass, (AssetTypeValueField?)Carrying(small)) },
                0L, $"the {Checks.WorldManagerClass} at pathId 9 carries 999 prefabs, want at least {Checks.MinPrefabs}",
                typeof(RefusalException), new[] { 9L }),
        };

        var failures = new List<string>();
        foreach ((string name, var assets, long want, string? wantProblem, Type? wantType, long[] wantRead) in cases)
        {
            var read = new List<long>();
            long pathId = 0;
            AssetTypeValueField? array = null;
            Exception? thrown = Record.Exception(() => (pathId, array) = Program.SourcePrefabs(
                assets.Select(asset => new AssetFileInfo { PathId = asset.PathId }),
                info => assets.Single(asset => asset.PathId == info.PathId).Script,
                info =>
                {
                    read.Add(info.PathId);
                    return assets.Single(asset => asset.PathId == info.PathId).Deserialized;
                },
                "resources.assets"));

            if (wantProblem != null)
            {
                if (thrown == null || thrown.GetType() != wantType || thrown.Message != wantProblem)
                {
                    failures.Add($"{name}: got {thrown?.ToString() ?? $"the roster at pathId {pathId}"}, want {wantType} {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want the roster at pathId {want}");
            }
            else if (pathId != want || !ReferenceEquals(array, roster))
            {
                failures.Add($"{name}: took the roster at pathId {pathId}, want the one at pathId {want}");
            }

            if (!read.SequenceEqual(wantRead))
            {
                failures.Add($"{name}: deserialized {string.Join(", ", read)}, want {string.Join(", ", wantRead)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Both lists are built by one walk in one order, so an index applied to the
    // wrong one reads a roster off a WorldManager whose pointers leave the file.
    // ChooseRoster leaves its index at 0 when it reports a problem, so a caller
    // reading it anyway takes a roster the checks had just rejected.
    [Fact]
    public void SelectRosterTakesTheArrayTheChoiceSettledOn()
    {
        (string Name, (long PathId, int External, int Total)[] Candidates, int Seen, int Want, string? WantProblem)[] cases =
        {
            ("the one self-contained roster of two", new[] { (7L, 3, 1565), (9L, 0, 1565) }, 2, 1, null),
            ("the one self-contained roster first", new[] { (9L, 0, 1565), (7L, 3, 1565) }, 2, 0, null),
            ("two self-contained rosters", new[] { (7L, 0, 1565), (9L, 0, 1565) }, 2, 0, "holds 2 self-contained"),
            ("no self-contained roster", new[] { (7L, 3, 1565) }, 1, 0, "entries point outside resources.assets"),
            ("a self-contained roster below the floor", new[] { (9L, 0, 999) }, 1, 0, "carries 999 prefabs, want at least 1000"),
            ("no WorldManager carrying SourcePrefabs", Array.Empty<(long, int, int)>(), 4, 0, "carries SourcePrefabs"),
            ("no WorldManager at all", Array.Empty<(long, int, int)>(), 0, 0, "holds no WorldManager at all"),
        };

        var failures = new List<string>();
        foreach ((string name, var candidates, int seen, int want, string? wantProblem) in cases)
        {
            AssetTypeValueField[] arrays = candidates
                .Select(_ => Node("Array", AssetValueType.Array, null))
                .ToArray();

            long pathId = 0;
            AssetTypeValueField? array = null;
            Exception? thrown = Record.Exception(() => (pathId, array) = Program.SelectRoster(candidates, arrays, "resources.assets", seen));

            if (wantProblem != null)
            {
                if (thrown is not RefusalException || !thrown.Message.Contains(wantProblem, StringComparison.Ordinal))
                {
                    failures.Add($"{name}: got {thrown?.Message ?? $"the roster at pathId {pathId}"}, want {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want the roster at index {want}");
            }
            else if (pathId != candidates[want].PathId || !ReferenceEquals(array, arrays[want]))
            {
                failures.Add($"{name}: took pathId {pathId}, want index {want} at pathId {candidates[want].PathId}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The roster that comes back is missing every entry that was refused, and
    // the caller reaches it only where none was, so no count taken over it can
    // be short without the refusal having been raised first.
    [Fact]
    public void RosterWalksPastAPrefabItRefusedAndRefusesAtTheEnd()
    {
        var unanticipated = new NullReferenceException();

        Program.Prefab Entry(int index) => Prefab($"Prefab{index}", index, index, "Assets.Scripts.Objects.Thing", null);

        (string Name, int Entries, Func<int, Exception?> Refuse, string[]? WantRead, string? WantProblem)[] cases =
        {
            ("a roster the walk read all of", 3, _ => null, new[] { "Prefab0", "Prefab1", "Prefab2" }, null),
            ("a roster with nothing on it", 0, _ => null, Array.Empty<string>(), null),
            ("one entry of many",
                4, index => index == 1 ? new RefusalException("the prefab Prefab1 at pathId 1 names no script type") : null,
                null,
                "the walk refused 1 of the 4 prefabs on the roster: the prefab Prefab1 at pathId 1 names no script type"),
            ("two entries far apart",
                901,
                index => index switch
                {
                    3 => new RefusalException("the prefab Prefab3 at pathId 3 names no script type"),
                    900 => new RefusalException("the prefab Prefab900 at pathId 900 serializes state flags this extraction does not model"),
                    _ => null,
                },
                null,
                "the walk refused 2 of the 901 prefabs on the roster: " +
                "the prefab Prefab3 at pathId 3 names no script type, " +
                "the prefab Prefab900 at pathId 900 serializes state flags this extraction does not model"),
            ("every entry refused", 2, index => new RefusalException($"the prefab Prefab{index} at pathId {index} names no script type"),
                null,
                "the walk refused 2 of the 2 prefabs on the roster: " +
                "the prefab Prefab0 at pathId 0 names no script type, the prefab Prefab1 at pathId 1 names no script type"),
            ("an entry that failed in a way this reader never anticipated",
                4, index => index == 1 ? unanticipated : null, null, null),
        };

        var failures = new List<string>();
        foreach ((string name, int entries, var refuse, string[]? wantRead, string? wantProblem) in cases)
        {
            List<Program.Prefab>? read = null;
            Exception? thrown = Record.Exception(() => read = Program.Roster(
                entries, index => refuse(index) is Exception refusal ? throw refusal : Entry(index)));

            if (wantProblem != null)
            {
                if (thrown is not RefusalException || thrown.Message != wantProblem)
                {
                    failures.Add($"{name}: got {thrown?.Message ?? "no refusal"}, want {wantProblem}");
                }
            }
            else if (wantRead == null)
            {
                if (!ReferenceEquals(thrown, unanticipated))
                {
                    failures.Add($"{name}: got {thrown?.ToString() ?? "no failure"}, want the failure itself");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want {wantRead.Length} prefabs");
            }
            else if (!read!.Select(prefab => prefab.Name).SequenceEqual(wantRead, StringComparer.Ordinal))
            {
                failures.Add($"{name}: read {string.Join(", ", read!.Select(prefab => prefab.Name))}, want {string.Join(", ", wantRead)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // An entry pointing at something that does not deserialize is refused by
    // naming the pointer, an asset that did not come back carrying no name to
    // call it by. The class driving the asset is named off the asset the pointer
    // landed on rather than off the roster's own file.
    [Fact]
    public void ReadPrefabFollowsTheEntryPointerToTheAssetItNames()
    {
        const string entryWhere = "entry 3 of the prefab roster at pathId 9";
        const string sensor = "Assets.Scripts.Objects.Pipes.Sensor";

        AssetExternal Deserialized(AssetTypeValueField asset) => new() { baseField = asset };

        (string Name, AssetTypeValueField Pptr, AssetExternal Read, string? Script, string? Want, string? WantProblem, string WantFollowed)[] cases =
        {
            ("the asset the pointer names", Pointer(1, 42), Deserialized(Asset()), sensor, "StructureSensor", null, "1:42"),
            ("an entry pointing at something that does not deserialize", Pointer(1, 42), default, sensor, null,
                "prefab 1:42 does not deserialize", "1:42"),
            ("an entry pointing inside the file it sits in", Pointer(0, 42), Deserialized(Asset()), sensor,
                "StructureSensor", null, "0:42"),
            ("an entry with no file id",
                Node("data", AssetValueType.None, null, Long(Checks.PathIdField, 42)), default, sensor, null,
                $"{entryWhere} has no {Checks.FileIdField} field", ""),
            ("an entry with no path id",
                Node("data", AssetValueType.None, null, Int(Checks.FileIdField, 1)), default, sensor, null,
                $"{entryWhere} has no {Checks.PathIdField} field", ""),
            ("an entry whose path id narrowed",
                Node("data", AssetValueType.None, null, Int(Checks.FileIdField, 1), Int(Checks.PathIdField, 42)),
                default, sensor, null,
                $"{entryWhere} serializes {Checks.PathIdField} as Int32 rather than as Int64", ""),
            ("an asset driven by no class at all", Pointer(1, 42), Deserialized(Asset()), null, null,
                "the prefab StructureSensor at pathId 42 names no script type", "1:42"),
        };

        var failures = new List<string>();
        foreach ((string name, var pptr, var answer, string? script, string? want, string? wantProblem, string wantFollowed) in cases)
        {
            var followed = new List<string>();
            Program.Prefab? got = null;
            Exception? thrown = Record.Exception(() => got = Program.ReadPrefab(
                pptr,
                entryWhere,
                (fileId, pathId) =>
                {
                    followed.Add($"{fileId}:{pathId}");
                    return answer;
                },
                _ => script));

            if (wantProblem != null)
            {
                if (thrown is not RefusalException || thrown.Message != wantProblem)
                {
                    failures.Add($"{name}: got {thrown?.Message ?? $"the prefab {got!.Name}"}, want {wantProblem}");
                }
            }
            else if (thrown != null)
            {
                failures.Add($"{name}: threw {thrown.Message}, want the prefab {want}");
            }
            else if (got!.Name != want || got.PathId != 42 || got.Script != script)
            {
                failures.Add($"{name}: read the prefab {got.Name} at pathId {got.PathId} driven by {Show(got.Script)}");
            }

            if (string.Join(", ", followed) != wantFollowed)
            {
                failures.Add($"{name}: followed {Show(string.Join(", ", followed))}, want {Show(wantFollowed)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The seven counts are pairwise distinct on this fixture, which is what
    // makes a line reading off a neighbour's property visible. Length and
    // distinct-name count cannot be separated: the game keys every prefab by a
    // hash of its name, so a build shipping two of one name does not start.
    [Fact]
    public void ReportCountsWhatTheWalkSaw()
    {
        var roster = new[]
        {
            Prefab("StructureSensor", 11, 1, "Assets.Scripts.Objects.Pipes.Sensor", 10f,
                new Program.Slot(1), new Program.Slot(2), new Program.Slot(3)),
            Prefab("StructureSensorMkII", 12, 2, "Assets.Scripts.Objects.Pipes.Sensor", 0f,
                new Program.Slot(4), new Program.Slot(5)),
            Prefab("StructureDaylightSensor", 13, 3, "Assets.Scripts.Objects.Pipes.Sensor", 5f, new Program.Slot(6)),
            Prefab("ToolWrench", 14, 4, "Assets.Scripts.Objects.Thing", 1f),
            Prefab("ItemWrench", 15, 5, "Assets.Scripts.Objects.Thing", null),
        };

        Assert.Equal(
            new[]
            {
                "unity version: 2022.3.41f1",
                "assembly version: 0.2.5445.24403",
                "prefabs: 5",
                "distinct script classes: 2",
                "prefabs declaring a power draw: 4",
                "prefabs with slots: 3",
                "slots: 6",
            },
            Program.Report("2022.3.41f1", "0.2.5445.24403", roster));
    }

    // The keys are the artifact's contract with tools/isagen, which nothing
    // either side spells twice: a renamed key reaches the decoder as an empty
    // name, a zero hash or an unpowered device. The written text is read back as
    // well as the value, a double differing only as the digits it spells.
    [Fact]
    public void WriteEmitsTheSchemaTheExtractionReads()
    {
        var prefabs = new[]
        {
            Prefab("StructureSensor", 11, 1812372242, "Assets.Scripts.Objects.Pipes.Sensor", 0.1f, new Program.Slot(2)),
            Prefab("StructurePanel", 12, 1110935274, "Objects.Structures.Panel", 0f),
            Prefab("ItemWrench", 13, -1886261558, "Assets.Scripts.Objects.Thing", null),
        };

        string path = TempPath("prefabs.json");
        Program.Write(path, "0.2.5445.24403", prefabs);
        using JsonDocument written = JsonDocument.Parse(File.ReadAllBytes(path));

        JsonElement root = written.RootElement;
        Assert.Equal(new[] { "assembly_version", "prefabs" }, Keys(root));
        Assert.Equal("0.2.5445.24403", root.GetProperty("assembly_version").GetString());

        JsonElement[] entries = root.GetProperty("prefabs").EnumerateArray().ToArray();
        Assert.Equal(3, entries.Length);

        Assert.Equal(new[] { "name", "hash", "script", "state", "used_power", "slots" }, Keys(entries[0]));
        Assert.Equal("StructureSensor", entries[0].GetProperty("name").GetString());
        Assert.Equal(1812372242, entries[0].GetProperty("hash").GetInt32());
        Assert.Equal("Assets.Scripts.Objects.Pipes.Sensor", entries[0].GetProperty("script").GetString());
        Assert.Equal(0.1f, entries[0].GetProperty("used_power").GetSingle());
        Assert.Equal("0.1", entries[0].GetProperty("used_power").GetRawText());
        Assert.Equal(new[] { "class" }, Keys(entries[0].GetProperty("slots").EnumerateArray().Single()));
        Assert.Equal(2, entries[0].GetProperty("slots").EnumerateArray().Single().GetProperty("class").GetInt32());

        Assert.Equal(Checks.StateFields, Keys(entries[0].GetProperty("state")));
        Assert.True(entries[0].GetProperty("state").GetProperty("HasPowerState").GetBoolean());
        Assert.False(entries[0].GetProperty("state").GetProperty("HasOpenState").GetBoolean());

        // A device drawing nothing keeps the key; only a thing that is not a
        // device loses it, and the two are different answers to
        // LogicType.RequiredPower. Neither entry below declares a slot and the
        // roster is written anyway, most of what the game ships holding nothing.
        Assert.Equal(new[] { "name", "hash", "script", "state", "used_power" }, Keys(entries[1]));
        Assert.Equal(0f, entries[1].GetProperty("used_power").GetSingle());

        // The third hash is below zero, the half of the roster an absolute or
        // unsigned writer disagrees on.
        Assert.Equal(new[] { "name", "hash", "script", "state" }, Keys(entries[2]));
        Assert.Equal("-1886261558", entries[2].GetProperty("hash").GetRawText());
    }

    // A roster where no prefab carries a draw is not a build without devices, it
    // is a build whose draw this reader stopped finding. The roster carries a
    // slot so the draw is the only thing missing, otherwise the sentence would
    // be the two coverage refusals joined.
    [Fact]
    public void WriteRefusesARosterWithNoDrawOnItAtAll()
    {
        string path = TempPath("prefabs.json");
        var roster = new[]
        {
            Prefab("ItemWrench", 11, -1886261558, "Assets.Scripts.Objects.Thing", null, new Program.Slot(2)),
            Prefab("ItemSpanner", 12, 1, "Assets.Scripts.Objects.Thing", null),
        };

        Assert.Equal(
            $"none of the 2 prefabs read carries a {Checks.PowerField}, so the whole roster would read as drawing nothing",
            Assert.IsType<RefusalException>(Record.Exception(() => Program.Write(path, "0.2.5445.24403", roster))).Message);
        Assert.False(File.Exists(path), "the refused roster was written anyway");
    }

    // The list is required on every prefab, so one that lost it is refused in
    // the walk. What is left over is a list every prefab still carries and none
    // fills, which no other check in this reader can see.
    [Fact]
    public void WriteRefusesARosterThatDeclaresNoSlotAtAll()
    {
        string path = TempPath("prefabs.json");
        var roster = new[]
        {
            Prefab("StructureSensor", 11, 1812372242, "Assets.Scripts.Objects.Pipes.Sensor", 10f),
            Prefab("ItemWrench", 12, -1886261558, "Assets.Scripts.Objects.Thing", null),
        };

        Assert.Equal(
            "none of the 2 prefabs read declares a slot, so the whole roster would read as holding nothing",
            Assert.IsType<RefusalException>(Record.Exception(() => Program.Write(path, "0.2.5445.24403", roster))).Message);
        Assert.False(File.Exists(path), "the refused roster was written anyway");
    }

    // The draw check was once behind both coverage checks. The middle row is the
    // input that showed it: one prefab carries a draw, so power coverage passes,
    // the slot question fires, and the unwritable draw was never named.
    [Fact]
    public void WriteNamesEveryProblemTheRosterHasAtOnce()
    {
        const string noDraw = "none of the 1 prefabs read carries a UsedPower, so the whole roster would read as drawing nothing";
        const string noSlot = "none of the 1 prefabs read declares a slot, so the whole roster would read as holding nothing";

        (string Name, Program.Prefab[] Roster, string Want)[] cases =
        {
            ("a roster with neither a draw nor a slot on it",
                new[] { Prefab("ItemWrench", 11, -1886261558, "Assets.Scripts.Objects.Thing", null) },
                noDraw + "; " + noSlot),
            ("a roster whose only draw is one JSON has no number for",
                new[] { Prefab("StructureSensor", 11, 1812372242, "Assets.Scripts.Objects.Pipes.Sensor", float.NaN) },
                noSlot + "; the roster carries 1 of 1 UsedPower values the artifact has no number for: " +
                "the prefab StructureSensor at pathId 11 carries NaN"),
            ("a roster with no slot on it and a draw JSON has no number for",
                new[]
                {
                    Prefab("StructureSensor", 11, 1812372242, "Assets.Scripts.Objects.Pipes.Sensor", float.NaN),
                    Prefab("ItemWrench", 12, -1886261558, "Assets.Scripts.Objects.Thing", null),
                },
                "none of the 2 prefabs read declares a slot, so the whole roster would read as holding nothing; " +
                "the roster carries 1 of 1 UsedPower values the artifact has no number for: " +
                "the prefab StructureSensor at pathId 11 carries NaN"),
        };

        var failures = new List<string>();
        foreach ((string name, var roster, string want) in cases)
        {
            string path = TempPath("prefabs.json");
            Exception? thrown = Record.Exception(() => Program.Write(path, "0.2.5445.24403", roster));
            if (thrown is not RefusalException || thrown.Message != want)
            {
                failures.Add($"{name}: got {thrown?.Message ?? "no refusal"}, want {want}");
            }
            else if (File.Exists(path))
            {
                failures.Add($"{name}: the refused roster was created anyway");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Utf8JsonWriter refuses NaN and both infinities at the field rather than
    // before the file, so the artifact is already created and holding one brace
    // by then, and its message names neither the prefab nor the field. The bad
    // draw sits between two good ones, the check running over the whole roster.
    [Fact]
    public void WriteRefusesADrawJsonHasNoNumberFor()
    {
        const string carries = "the roster carries ";
        const string noNumber = " values the artifact has no number for: ";

        (string Name, float[] Draws, string Want)[] cases =
        {
            ("not a number", new[] { float.NaN },
                $"{carries}1 of 3 {Checks.PowerField}{noNumber}the prefab StructureSensor at pathId 12 carries NaN"),
            ("an unbounded draw", new[] { float.PositiveInfinity },
                $"{carries}1 of 3 {Checks.PowerField}{noNumber}the prefab StructureSensor at pathId 12 carries Infinity"),
            ("an unbounded draw below zero", new[] { float.NegativeInfinity },
                $"{carries}1 of 3 {Checks.PowerField}{noNumber}the prefab StructureSensor at pathId 12 carries -Infinity"),
            ("two of them either side of one the writer could emit", new[] { float.NaN, float.NaN },
                $"{carries}2 of 4 {Checks.PowerField}{noNumber}" +
                "the prefab StructureSensor at pathId 12 carries NaN, the prefab StructureSensorMkII at pathId 14 carries NaN"),
        };

        var failures = new List<string>();
        foreach ((string name, float[] draws, string want) in cases)
        {
            string path = TempPath("prefabs.json");
            var roster = new List<Program.Prefab>
            {
                Prefab("StructurePanel", 11, 1110935274, "Objects.Structures.Panel", 10f, new Program.Slot(2)),
                Prefab("StructureSensor", 12, 1812372242, "Assets.Scripts.Objects.Pipes.Sensor", draws[0]),
                Prefab("StructureDaylightSensor", 13, 1076425094, "Assets.Scripts.Objects.Pipes.Sensor", 5f),
            };
            if (draws.Length > 1)
            {
                roster.Add(Prefab("StructureSensorMkII", 14, 1, "Assets.Scripts.Objects.Pipes.Sensor", draws[1]));
            }

            Exception? thrown = Record.Exception(() => Program.Write(path, "0.2.5445.24403", roster));
            if (thrown is not RefusalException || thrown.Message != want)
            {
                failures.Add($"{name}: got {thrown?.Message ?? "no refusal"}, want {want}");
            }
            else if (File.Exists(path))
            {
                failures.Add($"{name}: the refused roster was created anyway");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The three gates run before the writing starts, so a failure inside it is
    // past them. Downstream reads the artifact's name and nothing else, and
    // whether the process exited 1 is not something the file records. The
    // roster is long enough to have driven the writer's buffer to disk.
    [Fact]
    public void WriteLeavesNothingUnderTheArtifactsNameWhenItFailsPartWayThrough()
    {
        string path = TempPath("prefabs.json");

        var roster = new List<Program.Prefab>();
        for (int index = 0; index < 4000; index++)
        {
            roster.Add(Prefab($"StructurePanel{index}", index, index, "Objects.Structures.Panel", 10f, new Program.Slot(2)));
        }
        roster.Add(new Program.Prefab(
            "StructureSensor", 4000, 1812372242, "Assets.Scripts.Objects.Pipes.Sensor",
            new Dictionary<string, bool>(StringComparer.Ordinal), 10f, Array.Empty<Program.Slot>()));

        Assert.IsType<KeyNotFoundException>(
            Record.Exception(() => Program.Write(path, "0.2.5445.24403", roster)));
        Assert.False(File.Exists(path), "half an artifact was left under the name the pipeline reads");

        string staging = path + Program.Staging;
        Assert.True(File.Exists(staging), "nothing was written at all, so this proves nothing about where it went");
        Assert.True(new FileInfo(staging).Length > 0, "the staged copy is empty, so nothing reached the disk");
    }

    // The pipeline runs this reader over a depot it already has an artifact
    // from, so a move declining to land on a taken name fails every run after
    // the first. The staged copy is what a failed run leaves as evidence, so one
    // left by a run that succeeded reads as a failure that did not happen.
    [Fact]
    public void WriteReplacesTheArtifactAlreadyUnderThatName()
    {
        string path = TempPath("prefabs.json");
        Program.Prefab[] First() =>
            new[] { Prefab("StructurePanel", 11, 1110935274, "Objects.Structures.Panel", 10f, new Program.Slot(2)) };
        Program.Prefab[] Second() =>
            new[] { Prefab("ItemWrench", 12, -1886261558, "Assets.Scripts.Objects.Thing", 5f, new Program.Slot(3)) };

        Program.Write(path, "0.2.5445.24403", First());
        Program.Write(path, "0.2.5445.24404", Second());

        using JsonDocument written = JsonDocument.Parse(File.ReadAllBytes(path));
        Assert.Equal("0.2.5445.24404", written.RootElement.GetProperty("assembly_version").GetString());
        Assert.Equal(
            "ItemWrench",
            written.RootElement.GetProperty("prefabs").EnumerateArray().Single().GetProperty("name").GetString());
        Assert.False(File.Exists(path + Program.Staging), "a run that succeeded left a staged copy behind");
    }

    // The JSON writer's own line ending is the host's, so a reader built on
    // Windows would write the same roster differently. On a host whose line
    // ending is already this one the assertion passes either way; what it
    // catches is the reader built where that is not true.
    [Fact]
    public void TheArtifactIsWrittenTheSameWayWhereverItIsProduced()
    {
        string path = TempPath("prefabs.json");
        Program.Write(
            path,
            "0.2.5445.24403",
            new[] { Prefab("StructurePanel", 11, 1110935274, "Objects.Structures.Panel", 10f, new Program.Slot(2)) });

        Assert.DoesNotContain((byte)'\r', File.ReadAllBytes(path));
    }

    private static void Check((string Name, Action<List<AssetTypeValueField>> Mutate, string Want)[] cases)
    {
        var failures = new List<string>();
        foreach ((string name, var mutate, string want) in cases)
        {
            Exception? thrown = Record.Exception(() => Build(mutate));
            if (thrown == null)
            {
                failures.Add($"{name}: read without refusing, want {want}");
            }
            else if (thrown is not RefusalException || !thrown.Message.Contains(want, StringComparison.Ordinal))
            {
                failures.Add($"{name}: threw {thrown.Message}, want {want}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    private static Program.Prefab Build(Action<List<AssetTypeValueField>> mutate) =>
        Program.BuildPrefab(Asset(mutate), "Assets.Scripts.Objects.Pipes.Sensor", 42);

    private static Program.Prefab Prefab(string name, long pathId, int hash, string script, float? usedPower, params Program.Slot[] slots) =>
        new(name, pathId, hash, script,
            Checks.StateFields.ToDictionary(field => field, field => field == "HasPowerState", StringComparer.Ordinal),
            usedPower, slots);

    // A deserialized prefab of the shape the game ships. DamageState and
    // HasRunOnAtmospherics are neither read nor flags, and are here because
    // Thing serializes them either side of the flag block. The slot list repeats
    // a class because the game ships prefabs that do, such as a locker.
    private static AssetTypeValueField Asset(Action<List<AssetTypeValueField>>? mutate = null)
    {
        var fields = new List<AssetTypeValueField>
        {
            Str("PrefabName", "StructureSensor"),
            Int("PrefabHash", 1812372242),
            SlotArray(Ordinal(2), Ordinal(5), Ordinal(2)),
            Node("DamageState", AssetValueType.None, null, Float("MaxDamage", 200f)),
            Flag("HasRunOnAtmospherics", false),
        };
        fields.AddRange(Checks.ThingStateFields.Select(flag => Flag(flag, flag == "HasPowerState")));
        fields.Add(Float(Checks.PowerField, 10f));
        mutate?.Invoke(fields);
        return Node("Base", AssetValueType.None, null, fields.ToArray());
    }

    private static void Remove(List<AssetTypeValueField> fields, string name) =>
        fields.RemoveAll(field => field.FieldName == name);

    // Swaps the field of the same name so a case names one field and leaves the
    // rest of the prefab as the game ships it.
    private static void Replace(List<AssetTypeValueField> fields, AssetTypeValueField replacement)
    {
        int at = fields.FindIndex(field => field.FieldName == replacement.FieldName);
        if (at < 0)
        {
            throw new ArgumentException($"the fixture carries no {replacement.FieldName}", nameof(replacement));
        }
        fields[at] = replacement;
    }

    // One argument per slot, each holding the one field this reader takes.
    private static AssetTypeValueField SlotArray(params AssetTypeValueField[] slots) =>
        Node("Slots", AssetValueType.None, null,
            Node("Array", AssetValueType.Array, null,
                slots.Select(slot => Node("Slot", AssetValueType.None, null, slot)).ToArray()));

    private static AssetTypeValueField Ordinal(ushort value) =>
        Node("Type", AssetValueType.UInt16, new AssetTypeValue(value));

    private static AssetTypeValueField Str(string name, string value) =>
        Node(name, AssetValueType.String, new AssetTypeValue(value));

    private static AssetTypeValueField Int(string name, int value) =>
        Node(name, AssetValueType.Int32, new AssetTypeValue(value));

    private static AssetTypeValueField Long(string name, long value) =>
        Node(name, AssetValueType.Int64, new AssetTypeValue(value));

    private static AssetTypeValueField Float(string name, float value) =>
        Node(name, AssetValueType.Float, new AssetTypeValue(value));

    private static AssetTypeValueField Flag(string name, bool value) =>
        Node(name, AssetValueType.UInt8, new AssetTypeValue((byte)(value ? 1 : 0)));

    private static AssetTypeValueField Node(string name, AssetValueType type, AssetTypeValue? value, params AssetTypeValueField[] children) =>
        new()
        {
            TemplateField = new AssetTypeTemplateField
            {
                Name = name,
                Type = type.ToString(),
                ValueType = type,
                Children = new List<AssetTypeTemplateField>(),
            },
            Value = value,
            Children = children.ToList(),
        };

    // One entry per file id, holding the field the self-containment count is
    // taken off.
    private static AssetTypeValueField Pointers(params int[] fileIds) =>
        Node("Array", AssetValueType.Array, null,
            fileIds.Select(fileId => Node("data", AssetValueType.None, null, Int("m_FileID", fileId))).ToArray());

    // Both halves of the pointer, at the widths the engine declares them over.
    private static AssetTypeValueField Pointer(int fileId, long pathId) =>
        Node("data", AssetValueType.None, null, Int(Checks.FileIdField, fileId), Long(Checks.PathIdField, pathId));

    private static string[] Keys(JsonElement element) =>
        element.EnumerateObject().Select(property => property.Name).ToArray();

    private static string Show(AssetPPtr? pptr) => pptr == null ? "no entry" : $"{pptr.FileId}:{pptr.PathId}";

    private static string Show(string? value) => value == null ? "null" : $"\"{value}\"";

    private static string Show(Checks.SerializedExtent? extent) =>
        extent == null ? "no claim at all" : $"{extent.Value.FileSize} bytes, data at {extent.Value.DataOffset}";

    private static string Show(Checks.PackageExtent? extent) =>
        extent == null ? "no claim at all" : $"format version {extent.Value.Version}, {extent.Value.CompressedSize} bytes of class data";
}
