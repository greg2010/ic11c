using System;
using System.Buffers.Binary;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection.PortableExecutable;
using Mono.Cecil;
using Xunit;

namespace PrefabReader.Tests;

// DeclarationsTests works against a real assembly. The decisions themselves are
// tested without one in ChecksTests; what is held here is that the reading and
// the deciding speak the same vocabulary.
public class DeclarationsTests : TempDirectories
{
    [Fact]
    public void DeclaredFieldsCarryTheirClrTypeNames()
    {
        using AssemblyDefinition assembly = Fixture();

        Assert.Null(Declarations.InstanceFields(assembly, Checks.ThingClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? thing));
        Assert.NotNull(thing);
        Assert.All(Checks.ThingStateFields, name => Assert.Equal(new Checks.DeclaredField(Checks.FlagFieldType, true), thing[name]));
        Assert.Null(Checks.DeclaredFlagsProblem(thing));

        Assert.Null(Declarations.InstanceFields(assembly, Checks.DeviceClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? device));
        Assert.NotNull(device);
        Assert.Equal(new Checks.DeclaredField(Checks.PowerFieldType, true), device[Checks.PowerField]);
        Assert.Null(Checks.DeclaredPowerProblem(device));
    }

    [Fact]
    public void StaticFieldsAreNotInstanceFields()
    {
        using AssemblyDefinition assembly = Fixture();
        Assert.Null(Declarations.InstanceFields(assembly, Checks.DeviceClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? device));
        Assert.NotNull(device);
        Assert.DoesNotContain("AllDevicePrefabs", device.Keys);
    }

    // Each row keeps the name and the CLR type this reader asks for and stops
    // being written by Unity, which is the change the roster would read as every
    // device drawing nothing.
    [Fact]
    public void FieldsUnityDoesNotWriteAreNotSerialized()
    {
        (string Name, FieldAttributes Attributes, bool Serialized)[] cases =
        {
            ("public", FieldAttributes.Public, true),
            ("marked NonSerialized", FieldAttributes.Public | FieldAttributes.NotSerialized, false),
            ("made private", FieldAttributes.Private, false),
            ("made readonly", FieldAttributes.Public | FieldAttributes.InitOnly, false),
        };

        var failures = new List<string>();
        foreach ((string name, FieldAttributes attributes, bool serialized) in cases)
        {
            using AssemblyDefinition assembly = Fixture(powerAttributes: attributes);
            string? read = Declarations.InstanceFields(assembly, Checks.DeviceClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? device);
            if (device == null)
            {
                failures.Add($"{name}: the fields could not be read: {read}");
                continue;
            }

            Checks.DeclaredField field = device[Checks.PowerField];
            if (field.Type != Checks.PowerFieldType || field.Serialized != serialized)
            {
                failures.Add($"{name}: got {field}, want serialized {serialized}");
            }

            string? problem = Checks.DeclaredPowerProblem(device);
            bool refused = problem != null
                && problem.Contains($"declares {Checks.PowerField} where Unity does not serialize it", StringComparison.Ordinal);
            if (refused == serialized)
            {
                failures.Add($"{name}: got {problem ?? "no refusal"}, want {(serialized ? "none" : "a refusal naming the field Unity does not write")}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    // An auto-implemented property compiles to a private <Name>k__BackingField
    // and to no field of the property's own name, so the game goes on reading
    // UsedPower everywhere while the serialized file loses it entirely.
    [Fact]
    public void APropertyIsNotTheFieldThisReaderReads()
    {
        using AssemblyDefinition assembly = Fixture();
        FieldDefinition draw = assembly.MainModule.GetType(Checks.DeviceClass).Fields
            .Single(field => field.Name == Checks.PowerField);
        draw.Name = $"<{Checks.PowerField}>k__BackingField";
        draw.Attributes = FieldAttributes.Private;

        Assert.Null(Declarations.InstanceFields(assembly, Checks.DeviceClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? device));
        Assert.NotNull(device);
        Assert.Contains(
            $"{Checks.DeviceClass} declares no {Checks.PowerField}",
            Checks.DeclaredPowerProblem(device),
            StringComparison.Ordinal);
    }

    // The attribute is matched on its whole name. Unity fields routinely carry
    // attributes saying nothing about serialization and the game declares plenty
    // of its own, so matching the bare name reads a private draw as written.
    [Fact]
    public void APrivateFieldIsSerializedOnlyWhenItAsksToBe()
    {
        (string Name, string? Attribute, bool Serialized)[] cases =
        {
            ("marked SerializeField", "UnityEngine.SerializeField", true),
            ("marked with an attribute that says nothing about serialization", "UnityEngine.Tooltip", false),
            ("marked with an attribute of the same bare name in another namespace",
                "Assets.Scripts.SerializeField", false),
            ("marked with an attribute of the same bare name in the global namespace", "SerializeField", false),
            ("marked with nothing", null, false),
        };

        var failures = new List<string>();
        foreach ((string name, string? attribute, bool serialized) in cases)
        {
            using AssemblyDefinition assembly = Fixture(
                powerAttributes: FieldAttributes.Private, powerAttribute: attribute);
            string? read = Declarations.InstanceFields(assembly, Checks.DeviceClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? device);
            if (device == null)
            {
                failures.Add($"{name}: the fields could not be read: {read}");
                continue;
            }

            bool got = device[Checks.PowerField].Serialized;
            if (got != serialized)
            {
                failures.Add($"{name}: read as serialized {got}, want {serialized}");
            }
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void AGameThatMovedTheFieldsIsRefused()
    {
        using (AssemblyDefinition enumFlags = Fixture(flagType: "System.Byte"))
        {
            Assert.Null(Declarations.InstanceFields(enumFlags, Checks.ThingClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? thing));
            Assert.NotNull(thing);
            Assert.Contains("rather than System.Boolean", Checks.DeclaredFlagsProblem(thing), StringComparison.Ordinal);
        }

        using (AssemblyDefinition widened = Fixture(powerType: "System.Double"))
        {
            Assert.Null(Declarations.InstanceFields(widened, Checks.DeviceClass, out IReadOnlyDictionary<string, Checks.DeclaredField>? device));
            Assert.NotNull(device);
            Assert.Contains($"declares {Checks.PowerField} as System.Double", Checks.DeclaredPowerProblem(device), StringComparison.Ordinal);
        }
    }

    // IL allows one type two instance fields of one name where their signatures
    // differ; C# does not emit that. A reader keeping whichever came last would
    // answer off a field it did not choose.
    [Fact]
    public void AFieldDeclaredTwiceIsRefused()
    {
        using AssemblyDefinition assembly = Fixture();
        TypeDefinition thing = assembly.MainModule.GetType(Checks.ThingClass);
        thing.Fields.Add(new FieldDefinition(
            "HasPowerState", FieldAttributes.Public, assembly.MainModule.TypeSystem.Byte));
        thing.Fields.Add(new FieldDefinition(
            "HasErrorState", FieldAttributes.Public, assembly.MainModule.TypeSystem.Byte));

        Assert.Equal(
            $"{Checks.ThingClass} declares more than one instance field named HasErrorState, HasPowerState, " +
            "and nothing here says which one this reader is after",
            Declarations.InstanceFields(assembly, Checks.ThingClass, out _));
    }

    [Fact]
    public void AClassThatIsGoneIsNamedInTheRefusal()
    {
        using AssemblyDefinition assembly = Fixture();
        Assert.Equal(
            $"{Checks.AssemblyFile} declares no Assets.Scripts.Objects.Pipes.Widget",
            Declarations.InstanceFields(assembly, "Assets.Scripts.Objects.Pipes.Widget", out _));
    }

    // An assembly whose flags and whose draw both moved comes back naming both,
    // and the missing stamp is the third clause: a check reached only when its
    // neighbour passes is a check nothing exercises.
    [Fact]
    public void InspectAssemblyReportsEveryMovedDeclarationAtOnce()
    {
        string path = Write(Fixture(flagType: "System.Byte", powerAttributes: FieldAttributes.Public | FieldAttributes.NotSerialized));

        string? problem = Declarations.InspectAssembly(path, out string? version);

        Assert.Null(version);
        Assert.Equal(
            $"{Checks.ThingClass} declares " +
            $"{Checks.JoinNames(Checks.ThingStateFields.Select(flag => $"{flag} as System.Byte"))} " +
            $"rather than {Checks.FlagFieldType}; " +
            $"{Checks.DeviceClass} declares {Checks.PowerField} where Unity does not serialize it, " +
            "so every prefab would read as drawing nothing; " +
            NoResource(path),
            problem);
    }

    // A namespace move takes both classes with it, so the first being unreadable
    // must not decide whether the second is asked about.
    [Fact]
    public void InspectAssemblyReportsBothClassesThatAreGone()
    {
        AssemblyDefinition assembly = Fixture();
        foreach (string className in new[] { Checks.ThingClass, Checks.DeviceClass })
        {
            assembly.MainModule.Types.Remove(assembly.MainModule.GetType(className));
        }

        string path = Write(assembly);
        Assert.Equal(
            $"{Checks.AssemblyFile} declares no {Checks.ThingClass}; " +
            $"{Checks.AssemblyFile} declares no {Checks.DeviceClass}; " +
            NoResource(path),
            Declarations.InspectAssembly(path, out _));
    }

    // The refusal names the section that was looked in, because a sentence
    // naming a resource nothing had opened reads the same whether the resource
    // is there or not.
    [Fact]
    public void InspectAssemblyRefusesAnAssemblyWithNoVersionResource()
    {
        string path = Write(Fixture());

        string? problem = Declarations.InspectAssembly(path, out string? version);

        Assert.Null(version);
        Assert.Equal(NoResource(path), problem);
    }

    private static string NoResource(string path) =>
        $"{path} carries no {VersionResource.Name}, {Checks.NoBuild}: there is no .rsrc section";

    // This test project stamps its own PE with a file version whose four parts
    // are pairwise distinct and differ from its product version, so which
    // version was taken and in what order are both decided here.
    [Fact]
    public void InspectAssemblyStampsTheRosterWithTheFileVersion()
    {
        Assert.Null(Declarations.InspectAssembly(Write(VersionedFixture()), out string? version));
        Assert.Equal("1.2.3.4", version);
    }

    // A build stamps the managed version attributes and the Win32 resource from
    // one number, so the test above passes on a reader taking either. Rewriting
    // only the resource is the one way to make the two disagree.
    [Fact]
    public void InspectAssemblyStampsTheRosterOffTheResourceRatherThanTheMetadata()
    {
        string path = Write(VersionedFixture());
        Restamp(path, new Checks.FileVersion(9, 8, 7, 6));

        Assert.Null(Declarations.InspectAssembly(path, out string? version));
        Assert.Equal("9.8.7.6", version);
    }

    // Restamp rewrites a PE's VS_FIXEDFILEINFO in place, leaving the managed
    // metadata alone. The search is confined to .rsrc because those four
    // signature bytes also turn up aligned in this suite's own compiled code,
    // where a whole-file search rewrites a method body and asserts nothing.
    private static void Restamp(string path, Checks.FileVersion version)
    {
        byte[] image = File.ReadAllBytes(path);
        SectionHeader rsrc;
        using (FileStream file = File.OpenRead(path))
        using (var pe = new PEReader(file))
        {
            rsrc = pe.PEHeaders.SectionHeaders.Single(section => section.Name == ".rsrc");
        }

        int at = -1;
        for (int i = rsrc.PointerToRawData; i + 4 <= rsrc.PointerToRawData + rsrc.SizeOfRawData; i += 4)
        {
            if (BinaryPrimitives.ReadUInt32LittleEndian(image.AsSpan(i)) == 0xFEEF04BD)
            {
                at = i;
                break;
            }
        }
        Assert.True(at >= 0, $"{path} carries no VS_FIXEDFILEINFO to rewrite");

        BinaryPrimitives.WriteUInt32LittleEndian(
            image.AsSpan(at + 8), (uint)((version.Major << 16) | version.Minor));
        BinaryPrimitives.WriteUInt32LittleEndian(
            image.AsSpan(at + 12), (uint)((version.Build << 16) | version.Revision));
        File.WriteAllBytes(path, image);
    }

    // A file that is not a PE has no resource section either, so the version
    // gate is asked after the assembly reader is content with the file rather
    // than beside it. Otherwise this reports a missing resource.
    [Fact]
    public void InspectAssemblyNamesAFileOfThatNameThatIsNotAnAssembly()
    {
        string path = TempPath(Checks.AssemblyFile);
        File.WriteAllText(path, "MZ is as far as this goes");

        string? problem = Declarations.InspectAssembly(path, out string? version);

        Assert.Null(version);
        Assert.Equal(
            $"{path}: this game assembly does not read, so the roster names no build: " +
            "Format of the executable (.exe) or library (.dll) is invalid.",
            problem);
    }

    // The caught failures share no common base, so a gate holding one lets the
    // rest escape as traces. Cecil's own message is carried into every sentence
    // because the list stands for several failures and only the message says
    // which arrived.
    [Fact]
    public void AssemblyProblemNamesEveryWayTheAssemblyCannotBeOpened()
    {
        const string path = "/depot/Managed/Assembly-CSharp.dll";
        string Want(Exception thrown) =>
            $"{path}: this game assembly does not read, so the roster names no build: {thrown.Message}";

        var notAPe = new BadImageFormatException("Format of the executable (.exe) or library (.dll) is invalid.");
        var denied = new UnauthorizedAccessException($"Access to the path '{path}' is denied.");
        var truncated = new EndOfStreamException("Unable to read beyond the end of the stream.");
        var wrongFormat = new NotSupportedException("Cannot read this file.");

        (string Name, Exception Thrown, string? Want, bool Escapes)[] cases =
        {
            ("a file that is not a PE", notAPe, Want(notAPe), false),
            ("a file this process may not read", denied, Want(denied), false),
            ("a file that ends before the reader is done with it", truncated, Want(truncated), false),
            ("a file that is not of that format at all", wrongFormat, Want(wrongFormat), false),
            ("a machine too small for the file", new OutOfMemoryException(), null, true),
            ("a failure this reader never anticipated", new NullReferenceException(), null, true),
        };

        var failures = new List<string>();
        foreach ((string name, var thrown, string? wanted, bool escapes) in cases)
        {
            string? got = null;
            AssemblyDefinition? opened = null;
            Exception raised = Record.Exception(() =>
                got = Declarations.AssemblyProblem(path, _ => throw thrown, out opened));

            if (escapes)
            {
                if (!ReferenceEquals(raised, thrown))
                {
                    failures.Add($"{name}: got {raised?.ToString() ?? "no failure"}, want the failure itself");
                }
            }
            else if (raised != null)
            {
                failures.Add($"{name}: threw {raised.Message}, want {wanted}");
            }
            else if (got != wanted)
            {
                failures.Add($"{name}: got {got ?? "no problem"}, want {wanted}");
            }
            else if (opened != null)
            {
                failures.Add($"{name}: answered an assembly beside the problem");
            }
        }

        using AssemblyDefinition assembly = Fixture();
        string? problem = Declarations.AssemblyProblem(path, _ => assembly, out AssemblyDefinition? read);
        if (problem != null || !ReferenceEquals(read, assembly))
        {
            failures.Add($"an assembly that opens: got {problem ?? "the wrong assembly"}, want the one that was opened");
        }

        // ReadAssembly either answers with an assembly or throws. Taken as a
        // delegate the seam admits a third answer, which would reach
        // InspectAssembly as an assembly nothing was said to be wrong with.
        Exception? nothing = Record.Exception(() => Declarations.AssemblyProblem(path, _ => null!, out _));
        string wantNothing =
            $"{path}: opening this game assembly answered nothing and raised nothing, which is neither thing it does";
        if (nothing is not InvalidOperationException || nothing.Message != wantNothing)
        {
            failures.Add($"an open that answered nothing: got {nothing?.ToString() ?? "no failure"}, want {wantNothing}");
        }
        Assert.True(failures.Count == 0, string.Join("\n", failures));
    }

    [Fact]
    public void InspectAssemblyNamesAnAssemblyThatIsNotThere()
    {
        string path = TempPath(Checks.AssemblyFile);

        string? problem = Declarations.InspectAssembly(path, out string? version);

        Assert.Null(version);
        Assert.Equal(
            $"{path}: no {Checks.AssemblyFile} beside the serialized files, so the roster names no build",
            problem);
    }

    // Fixture builds an assembly shaped like the two classes this reader asks
    // about. A null type keeps the field the game declares it as. Cecil creates
    // an assembly carrying no Win32 version resource, so every fixture from here
    // is also one the version gate refuses.
    private static AssemblyDefinition Fixture(
        string? flagType = null,
        string? powerType = null,
        FieldAttributes powerAttributes = FieldAttributes.Public,
        string? powerAttribute = null)
    {
        AssemblyDefinition assembly = AssemblyDefinition.CreateAssembly(
            new AssemblyNameDefinition("Assembly-CSharp", new Version(1, 0)), "Assembly-CSharp", ModuleKind.Dll);
        Declare(assembly.MainModule, flagType, powerType, powerAttributes, powerAttribute);
        return assembly;
    }

    // VersionedFixture grafts the same two classes onto this suite's own PE.
    // Cecil carries through the resource an assembly it rewrites already had, so
    // reaching the success path needs a base some build stamped.
    private static AssemblyDefinition VersionedFixture()
    {
        AssemblyDefinition assembly = AssemblyDefinition.ReadAssembly(typeof(DeclarationsTests).Assembly.Location);
        Declare(assembly.MainModule);
        return assembly;
    }

    private static void Declare(
        ModuleDefinition module,
        string? flagType = null,
        string? powerType = null,
        FieldAttributes powerAttributes = FieldAttributes.Public,
        string? powerAttribute = null)
    {
        TypeDefinition thing = AddType(module, Checks.ThingClass);
        foreach (string name in Checks.ThingStateFields)
        {
            thing.Fields.Add(new FieldDefinition(name, FieldAttributes.Public, Named(module, flagType) ?? module.TypeSystem.Boolean));
        }
        thing.Fields.Add(new FieldDefinition("IsDisabled", FieldAttributes.Public, module.TypeSystem.Boolean));

        TypeDefinition device = AddType(module, Checks.DeviceClass);
        var power = new FieldDefinition(Checks.PowerField, powerAttributes, Named(module, powerType) ?? module.TypeSystem.Single);
        if (powerAttribute != null)
        {
            power.CustomAttributes.Add(new CustomAttribute(AttributeConstructor(module, powerAttribute)));
        }
        device.Fields.Add(power);
        device.Fields.Add(new FieldDefinition("AllDevicePrefabs", FieldAttributes.Public | FieldAttributes.Static, module.TypeSystem.Object));
    }

    private string Write(AssemblyDefinition assembly)
    {
        string path = TempPath(Checks.AssemblyFile);
        using (assembly)
        {
            assembly.Write(path);
        }
        return path;
    }

    // Nothing resolves the reference and only the attribute's full name is ever
    // read, so a type declared nowhere serves as well as one that is.
    private static MethodReference AttributeConstructor(ModuleDefinition module, string fullName)
    {
        var attribute = new TypeReference(
            Namespace(fullName), Bare(fullName), module, module.TypeSystem.CoreLibrary);
        return new MethodReference(".ctor", module.TypeSystem.Void, attribute) { HasThis = true };
    }

    private static TypeDefinition AddType(ModuleDefinition module, string className)
    {
        var type = new TypeDefinition(
            Namespace(className),
            Bare(className),
            TypeAttributes.Public | TypeAttributes.Class,
            module.TypeSystem.Object);
        module.Types.Add(type);
        return type;
    }

    private static TypeReference? Named(ModuleDefinition module, string? fullName)
    {
        return fullName == null
            ? null
            : new TypeReference(
                Namespace(fullName),
                Bare(fullName),
                module,
                module.TypeSystem.CoreLibrary,
                valueType: true);
    }

    // The CLR keeps a qualified name in two pieces. A name with no dot is the
    // global namespace, which the game does declare classes in.
    private static string Namespace(string fullName)
    {
        int split = fullName.LastIndexOf('.');
        return split < 0 ? string.Empty : fullName.Substring(0, split);
    }

    private static string Bare(string fullName) => fullName.Substring(fullName.LastIndexOf('.') + 1);
}
