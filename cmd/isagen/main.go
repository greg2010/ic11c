// Command isagen builds the IC10 instruction and operand tables that
// internal/ic10 and internal/vm expose.
//
// The extract subcommand reads the Stationeers assembly, decompiled to C#
// beforehand, and writes internal/ic10/isa.json. The generate subcommand turns
// that JSON into the Go tables, into the C prelude an editor reads a MicroC
// program through, and into the interpreter's enum tables. Every output is
// checked in, so ordinary builds run neither.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Default locations of the checked-in artifacts, relative to the module root,
// which is where the isa task runs.
//
// The tables, the prelude, and the prelude's flags file sit together rather
// than anywhere more convenient to an editor, because they are one view of one
// input and drift is only diffable where check:codegen looks. The MicroC corpus
// gets an argument file of its own, which names that same header, so an editor
// opening a fixture is configured and there is still one header in the tree.
const (
	defaultJSONPath         = "internal/ic10/isa.json"
	defaultDevicesJSONPath  = "internal/ic10/devices.json"
	defaultTablesPath       = "internal/ic10/tables.gen.go"
	defaultDevicesPath      = "internal/ic10/devices.gen.go"
	defaultPreludePath      = "internal/ic10/" + preludeFileName
	defaultFlagsPath        = "internal/ic10/compile_flags.txt"
	defaultFixtureFlagsPath = "internal/parser/testdata/compile_flags.txt"
	defaultBasicEnumsPath   = "internal/vm/basicenums.gen.go"
)

// defaultDigestPath is the checked-in fingerprint of the game C# behind the
// hand-written interpreter, backend limits and quirk list.
//
// It sits away from the generated artifacts above on purpose: check:codegen
// diffs those paths and would turn a deliberate re-fingerprinting into a build
// failure, and the digest is a review aid rather than a gate.
const defaultDigestPath = "tools/gamesrc/csharp.digest"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "isagen: %v\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "isagen",
		Short: "Build the IC10 instruction and operand tables",
		Long: `isagen recovers the IC10 machine tables from the Stationeers assembly.

extract reads a decompiled copy of the assembly into ` + defaultJSONPath + `,
and devices reads the same copy into ` + defaultDevicesJSONPath + `, which
describes the game things a program names by hash rather than through the
instruction set. generate renders ` + defaultTablesPath + `,
` + defaultDevicesPath + `, ` + defaultPreludePath + `, ` + defaultFlagsPath + `,
` + defaultFixtureFlagsPath + ` and ` + defaultBasicEnumsPath + ` from that
JSON, and needs nothing else. Every output is checked in.

devices and generate both refuse a pair of JSON files whose manifest and
version disagree, which is what holds the two to one game build.

digest fingerprints the game types that hand-written Go transliterates into
` + defaultDigestPath + `, and dump writes their decompiled source out for
diffing. Neither is part of the build; see docs/game-updates.md.`,
		// main reports the error with the binary's own prefix; without this
		// cobra prints it a second time.
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		// A bare invocation fails rather than printing help and exiting zero,
		// so a caller that forgets the subcommand notices.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Usage(); err != nil {
				return err
			}
			return errors.New("expected a subcommand")
		},
	}
	root.AddCommand(extractCmd(), devicesCmd(), generateCmd(), digestCmd(), dumpCmd())
	return root
}

func extractCmd() *cobra.Command {
	var source, assembly, manifest, out string

	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Read decompiled game source into " + defaultJSONPath,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return extract(source, assembly, manifest, out)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "directory of decompiled C#, one file per type under its namespace")
	cmd.Flags().StringVar(&assembly, "assembly", "", "assembly the source was decompiled from, read for its file version")
	cmd.Flags().StringVar(&manifest, "manifest", "", "gid of the Steam depot manifest the assembly came from")
	cmd.Flags().StringVar(&out, "out", defaultJSONPath, "path of the ISA JSON to write")
	return cmd
}

func devicesCmd() *cobra.Command {
	var in deviceInputs
	var out string

	cmd := &cobra.Command{
		Use:   "devices",
		Short: "Read decompiled game source and the prefab roster into " + defaultDevicesJSONPath,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return devices(in, out)
		},
	}
	cmd.Flags().StringVar(&in.sourceDir, "source", "", "directory of decompiled C#, one file per type under its namespace")
	cmd.Flags().StringVar(&in.assembly, "assembly", "", "assembly the source was decompiled from, read for its file version")
	cmd.Flags().StringVar(&in.manifest, "manifest", "", "gid of the Steam depot manifest the assembly came from")
	cmd.Flags().StringVar(&in.prefabs, "prefabs", "", "path of the prefab roster tools/prefabreader wrote from the game's serialized files")
	cmd.Flags().StringVar(&in.names, "names", "", "path of the English localization XML, read for prefab titles")
	cmd.Flags().StringVar(&in.isa, "isa", defaultJSONPath, "path of the ISA JSON the result must agree with")
	cmd.Flags().StringVar(&out, "out", defaultDevicesJSONPath, "path of the device JSON to write")
	return cmd
}

func generateCmd() *cobra.Command {
	var in, devicesIn string
	var out outputs

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Render the Go tables and the C prelude from the checked-in ISA and device JSON",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return generate(in, devicesIn, out)
		},
	}
	cmd.Flags().StringVar(&in, "in", defaultJSONPath, "path of the ISA JSON to read")
	cmd.Flags().StringVar(&devicesIn, "devices-in", defaultDevicesJSONPath, "path of the device JSON to read")
	cmd.Flags().StringVar(&out.tables, "out", defaultTablesPath, "path of the Go tables to write")
	cmd.Flags().StringVar(&out.devices, "devices", defaultDevicesPath, "path of the Go device tables to write")
	cmd.Flags().StringVar(&out.prelude, "prelude", defaultPreludePath, "path of the C prelude header to write")
	cmd.Flags().StringVar(&out.flags, "compile-flags", defaultFlagsPath, "path of the C argument file to write beside the prelude")
	cmd.Flags().StringVar(&out.fixtureFlags, "fixture-compile-flags", defaultFixtureFlagsPath, "path of the C argument file to write beside the MicroC corpus")
	cmd.Flags().StringVar(&out.basicEnums, "basic-enums", defaultBasicEnumsPath, "path of the interpreter enum tables to write")
	return cmd
}

func digestCmd() *cobra.Command {
	var source, assembly, manifest, out string

	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Fingerprint the game types hand-written Go transliterates into " + defaultDigestPath,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return writeDigest(source, assembly, manifest, out)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "directory of decompiled C#, one file per type under its namespace")
	cmd.Flags().StringVar(&assembly, "assembly", "", "assembly the source was decompiled from, read for its file version")
	cmd.Flags().StringVar(&manifest, "manifest", "", "gid of the Steam depot manifest the assembly came from")
	cmd.Flags().StringVar(&out, "out", defaultDigestPath, "path of the digest to write")
	return cmd
}

func dumpCmd() *cobra.Command {
	var source, out string

	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Write the decompiled source of the fingerprinted types out for diffing",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return copyGameSource(source, out)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "directory of decompiled C#, one file per type under its namespace")
	cmd.Flags().StringVar(&out, "out", "", "directory to write the decompiled types into")
	return cmd
}
