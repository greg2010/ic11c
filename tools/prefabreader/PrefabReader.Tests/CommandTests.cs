using System;
using System.Collections.Generic;
using System.IO;
using Xunit;

namespace PrefabReader.Tests;

public class CommandTests
{
    private const string Usage = "usage: prefabreader <data-dir> <classdata.tpk> <out.json>";

    private static readonly string[] Three = { "depot", "classdata.tpk", "prefabs.json" };

    // Arity is the only check available: nothing in a path says which of the
    // three it is, so a run given two or four reads one argument as another.
    [Fact]
    public void CommandRefusesTheArgumentsItCannotWalkWith()
    {
        (string Name, string[] Args, int Want, string[]? WantWalked)[] cases =
        {
            ("nothing at all", Array.Empty<string>(), 2, null),
            ("one of the three", new[] { "depot" }, 2, null),
            ("two of the three", new[] { "depot", "classdata.tpk" }, 2, null),
            ("one argument too many", new[] { "depot", "classdata.tpk", "prefabs.json", "again" }, 2, null),
            ("the three it needs", Three, 0, Three),
        };

        var failures = new List<string>();
        foreach ((string name, string[] args, int want, string[]? wantWalked) in cases)
        {
            string[]? walked = null;
            (int got, string said) = Run(args, (dataDir, classPackage, outPath) =>
                walked = new[] { dataDir, classPackage, outPath });

            if (got != want)
            {
                failures.Add($"{name}: exited {got}, want {want}");
            }
            else if (wantWalked == null)
            {
                if (walked != null)
                {
                    failures.Add($"{name}: walked {string.Join(", ", walked)}, want no walk at all");
                }
                else if (said.TrimEnd() != Usage)
                {
                    failures.Add($"{name}: said {said.TrimEnd()}, want {Usage}");
                }
            }
            else if (walked == null)
            {
                failures.Add($"{name}: did not walk at all, want {string.Join(", ", wantWalked)}");
            }
            else if (!walked.AsSpan().SequenceEqual(wantWalked))
            {
                failures.Add($"{name}: walked {string.Join(", ", walked)}, want {string.Join(", ", wantWalked)}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // Both exit 1, so what is printed is the whole difference between a build
    // log naming the check that was met and one naming the line it died on.
    [Fact]
    public void CommandTellsARefusalFromAFailureItNeverAnticipated()
    {
        const string refusal = "none of the 1565 prefabs read declares a slot, so the whole roster would read as holding nothing";

        var failures = new List<string>();

        (int code, string said) = Run(Three, (_, _, _) => throw new RefusalException(refusal));
        if (code != 1)
        {
            failures.Add($"a refusal: exited {code}, want 1");
        }
        if (said.TrimEnd() != "prefabreader: " + refusal)
        {
            failures.Add($"a refusal: said {said.TrimEnd()}, want the sentence alone");
        }

        (code, said) = Run(Three, (_, _, _) => throw new NullReferenceException());
        if (code != 1)
        {
            failures.Add($"a failure this reader never anticipated: exited {code}, want 1");
        }
        if (!said.Contains(nameof(NullReferenceException), StringComparison.Ordinal))
        {
            failures.Add($"a failure this reader never anticipated: said {said.TrimEnd()}, want the type named");
        }
        if (!said.TrimEnd().Contains('\n'))
        {
            failures.Add($"a failure this reader never anticipated: said {said.TrimEnd()}, want the trace beside it");
        }

        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // The stream is handed in rather than swapped onto the process, because
    // xunit runs test classes beside one another and a swap would decide where
    // every one of them wrote.
    private static (int Code, string Said) Run(string[] args, Action<string, string, string> walk)
    {
        var said = new StringWriter();
        return (Program.Command(args, said, walk), said.ToString());
    }
}
