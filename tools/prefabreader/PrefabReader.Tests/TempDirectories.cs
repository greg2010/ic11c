using System;
using System.Collections.Generic;
using System.IO;

namespace PrefabReader.Tests;

// TempDirectories gives a test scratch directories and takes them back when it
// ends. A base class rather than a helper because xunit builds a fresh instance
// of a test class per test method and disposes it when the method returns, so a
// directory taken here cannot outlive the test that took it.
public abstract class TempDirectories : IDisposable
{
    private readonly List<string> taken = new();

    protected string TempDirectory()
    {
        string path = Directory.CreateTempSubdirectory().FullName;
        taken.Add(path);
        return path;
    }

    // TempPath names a file inside a new empty directory of this test's own.
    // Nothing is created at the path itself, so until the test writes there it
    // names a file that does not exist.
    protected string TempPath(string name) => Path.Combine(TempDirectory(), name);

    // Dispose attempts every directory whatever became of the ones before it,
    // and raises what failed together. xunit reports that as the test failing,
    // which is intended: a directory this suite cannot take back is a defect.
    public void Dispose()
    {
        var failures = new List<string>();
        foreach (string path in taken)
        {
            try
            {
                Directory.Delete(path, recursive: true);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                failures.Add($"{path}: {e.Message}");
            }
        }
        taken.Clear();

        if (failures.Count > 0)
        {
            throw new IOException(
                $"this test left {failures.Count} temporary directories behind: {string.Join("; ", failures)}");
        }
    }
}
