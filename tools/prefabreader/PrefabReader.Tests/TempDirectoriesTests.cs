using System;
using System.IO;
using System.Linq;
using Xunit;

namespace PrefabReader.Tests;

// TempDirectoriesTests asserts what disposal does, so each test disposes the
// probe itself rather than letting xunit do it afterwards, and again in a
// finally so that an assertion failing first does not leak what it was
// counting.
public class TempDirectoriesTests
{
    // Probe reaches the protected members from outside the hierarchy.
    private sealed class Probe : TempDirectories
    {
        internal string NewDirectory() => TempDirectory();

        internal string NewPath(string name) => TempPath(name);
    }

    // The three fixtures are the shapes this suite builds. The last is a tree
    // with a directory inside it, which a delete that was not recursive refuses
    // rather than removes.
    [Fact]
    public void TempDirectoriesTakesBackEverythingItGaveOut()
    {
        var probe = new Probe();
        try
        {
            string empty = probe.NewDirectory();
            string artifact = probe.NewPath("prefabs.json");
            File.WriteAllText(artifact, "{}");
            string depot = probe.NewDirectory();
            Directory.CreateDirectory(Path.Combine(depot, "Resources"));
            File.WriteAllBytes(Path.Combine(depot, "Resources", "unity_builtin_extra"), new byte[] { 0 });
            File.WriteAllBytes(Path.Combine(depot, Checks.ResourcesFile), new byte[] { 0 });

            string[] made = { empty, Holding(artifact), depot };
            string[] missing = made.Where(path => !Directory.Exists(path)).ToArray();
            Assert.True(missing.Length == 0, $"never created: {string.Join(", ", missing)}");

            probe.Dispose();

            string[] left = made.Where(Directory.Exists).ToArray();
            Assert.True(left.Length == 0, $"left behind: {string.Join(", ", left)}");
        }
        finally
        {
            probe.Dispose();
        }
    }

    // A directory already gone is the only cleanup failure reachable without
    // taking a permission away from the process, and it stands for the rest.
    [Fact]
    public void TempDirectoriesTakesBackTheRestWhenOneOfThemCannotBeTakenBack()
    {
        var probe = new Probe();
        try
        {
            string first = probe.NewDirectory();
            string gone = probe.NewDirectory();
            string last = probe.NewDirectory();
            Directory.Delete(gone, recursive: true);

            Exception thrown = Record.Exception(probe.Dispose);

            Assert.IsType<IOException>(thrown);
            Assert.Contains("left 1 temporary directories behind", thrown.Message, StringComparison.Ordinal);
            Assert.Contains(gone, thrown.Message, StringComparison.Ordinal);

            string[] left = new[] { first, last }.Where(Directory.Exists).ToArray();
            Assert.True(left.Length == 0, $"stopped at the failure and left behind: {string.Join(", ", left)}");
        }
        finally
        {
            probe.Dispose();
        }
    }

    [Fact]
    public void TempDirectoriesGivesEachFixtureItsOwnDirectory()
    {
        var probe = new Probe();
        try
        {
            var handed = new[]
            {
                probe.NewDirectory(),
                probe.NewDirectory(),
                Holding(probe.NewPath("prefabs.json")),
                Holding(probe.NewPath("prefabs.json")),
            };

            Assert.Equal(handed.Length, handed.Distinct(StringComparer.Ordinal).Count());
            Assert.All(handed, path => Assert.Empty(Directory.EnumerateFileSystemEntries(path)));
        }
        finally
        {
            probe.Dispose();
        }
    }

    // GetDirectoryName is null only for a root path, which this is not.
    private static string Holding(string path) => Path.GetDirectoryName(path)!;
}
