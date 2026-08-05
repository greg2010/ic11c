using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection.PortableExecutable;
using Mono.Cecil;

namespace PrefabReader;

/// <summary>
/// What the game assembly itself declares, as opposed to what its serialized files say: the
/// build stamp, and whether classes read by name still declare those fields the same way.
/// </summary>
internal static class Declarations
{
    // Marks a non-public field the engine serializes anyway -- the one way a field outside this
    // reader's normal reach can still be written.
    private const string SerializeFieldAttribute = "UnityEngine.SerializeField";

    /// <summary>
    /// Returns why the assembly beside the serialized files cannot stamp the roster, or null
    /// with <paramref name="version"/> set to its four-part version.
    /// </summary>
    internal static string? InspectAssembly(string path, out string? version)
    {
        version = null;
        if (!File.Exists(path))
        {
            return $"{path}: no {Checks.AssemblyFile} beside the serialized files, so the roster names no build";
        }

        string? opening = AssemblyProblem(path, AssemblyDefinition.ReadAssembly, out AssemblyDefinition? assembly);
        if (assembly == null)
        {
            return opening;
        }

        using (assembly)
        {
            string? problem = Checks.JoinProblems(
                DeclarationProblems(assembly),
                Stamp(path, out Checks.FileVersion stamp));
            if (problem != null)
            {
                return problem;
            }
            version = Checks.AssemblyVersion(stamp);
        }
        return null;
    }

    // Reports why the assembly carries no build stamp the roster can be held to, or null with
    // stamp set to its four-part version (meaningless otherwise). Opens the file a second time
    // since the managed metadata the framework offers instead answers a different question; see
    // VersionResource.
    private static string? Stamp(string path, out Checks.FileVersion stamp)
    {
        stamp = default;
        string? reason;
        try
        {
            using FileStream file = File.OpenRead(path);
            using var image = new PEReader(file);
            reason = VersionResource.Read(image, out stamp);
        }
        catch (Exception e) when (Checks.Unopenable(e))
        {
            return Unreadable(path, e);
        }
        return reason == null
            ? Checks.AssemblyVersionProblem(stamp, path)
            : $"{path} carries no {VersionResource.Name}, {Checks.NoBuild}: {reason}";
    }

    /// <summary>
    /// Returns why the file the assembly was expected in could not be opened, or null with
    /// <paramref name="assembly"/> set to what opening it produced.
    /// </summary>
    internal static string? AssemblyProblem(
        string path, Func<string, AssemblyDefinition> open, out AssemblyDefinition? assembly)
    {
        assembly = null;
        try
        {
            assembly = open(path);
        }
        catch (Exception e) when (Checks.Unopenable(e))
        {
            return Unreadable(path, e);
        }
        if (assembly == null)
        {
            throw new InvalidOperationException(
                $"{path}: opening this game assembly answered nothing and raised nothing, which is neither thing it does");
        }
        return null;
    }

    // Both openings of the assembly report an unreadable file through here, carrying the
    // library's own message so a build-log reader sees the same thing about it either way.
    private static string Unreadable(string path, Exception failure) =>
        $"{path}: this game assembly does not read, so the roster names no build: {failure.Message}";

    // Every problem the two classes have at once, or null when neither has one. Read and joined
    // in one pass rather than chained, since a game update moving one commonly moves the other.
    private static string? DeclarationProblems(AssemblyDefinition assembly) =>
        Checks.JoinProblems(
            ClassProblem(assembly, Checks.ThingClass, Checks.DeclaredFlagsProblem),
            ClassProblem(assembly, Checks.DeviceClass, Checks.DeclaredPowerProblem));

    // Why one class no longer declares what this reader reads off it, or null when it still does.
    private static string? ClassProblem(
        AssemblyDefinition assembly,
        string className,
        Func<IReadOnlyDictionary<string, Checks.DeclaredField>, string?> decide)
    {
        string? problem = InstanceFields(assembly, className, out IReadOnlyDictionary<string, Checks.DeclaredField>? fields);
        return fields == null ? problem : decide(fields);
    }

    /// <summary>
    /// Returns why a class's instance fields cannot be read by name, or null with
    /// <paramref name="fields"/> set to its own declared fields; a name IL allows twice is refused.
    /// </summary>
    internal static string? InstanceFields(
        AssemblyDefinition assembly, string className, out IReadOnlyDictionary<string, Checks.DeclaredField>? fields)
    {
        fields = null;
        TypeDefinition type = assembly.MainModule.GetType(className);
        if (type == null)
        {
            return $"{Checks.AssemblyFile} declares no {className}";
        }

        var declared = new Dictionary<string, Checks.DeclaredField>(StringComparer.Ordinal);
        var repeated = new HashSet<string>(StringComparer.Ordinal);
        foreach (FieldDefinition field in type.Fields.Where(field => !field.IsStatic))
        {
            if (!declared.TryAdd(field.Name, new Checks.DeclaredField(field.FieldType.FullName, Serialized(field))))
            {
                repeated.Add(field.Name);
            }
        }
        if (repeated.Count > 0)
        {
            return $"{className} declares more than one instance field named {Checks.JoinNames(repeated)}, " +
                "and nothing here says which one this reader is after";
        }

        fields = declared;
        return null;
    }

    // The engine's own serialization rule: written when public or SerializeField-marked, not
    // when read-only or NotSerialized. Type is not asked about here since both fields this reader
    // holds are primitives, and a type change is what the CLR type name in DeclaredField reports.
    private static bool Serialized(FieldDefinition field)
    {
        if (field.IsNotSerialized || field.IsInitOnly)
        {
            return false;
        }
        return field.IsPublic || field.CustomAttributes.Any(
            attribute => attribute.AttributeType.FullName == SerializeFieldAttribute);
    }
}
