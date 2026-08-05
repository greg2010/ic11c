// Command chipgen assembles a standalone, runnable copy of the game's own
// IC10 chip out of the decompiled Stationeers source. It is the game's own
// code in a compile unit needing nothing but the base class library, not a
// transliteration a hand-written oracle would risk getting wrong.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Default locations, relative to the module root where the task target
// runs. The output goes beside the decompile it is cut from, not under a
// package, since it is a derived, not-checked-in copy of third-party source.
const (
	defaultSourcePath = "gamesrc/source-full"
	defaultOutputPath = "gamesrc/chip"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "chipgen: %v\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "chipgen",
		Short: "Cut a runnable IC10 chip out of the decompiled game source",
		Long: `chipgen reads the decompiled Stationeers assembly and writes a C# compile
unit that builds against mscorlib and System alone.

What comes out is the game's ProgrammableChip, its operation classes, the
device permission and batch reduction bodies, and the reagent lookup, all
copied as written. The changes made to them are enumerated in the emitted
header and nowhere else.

The unit does not compile itself; ` + defaultOutputPath + ` is input to a Mono
build the task target runs. Nothing chipgen writes is checked in.`,
		// main reports the error with the binary's own prefix; without this
		// cobra prints it a second time.
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Usage(); err != nil {
				return err
			}
			return fmt.Errorf("expected a subcommand")
		},
	}
	root.AddCommand(sliceCmd())
	return root
}

func sliceCmd() *cobra.Command {
	var source, out string
	var quiet, update bool

	cmd := &cobra.Command{
		Use:   "slice",
		Short: "Write the standalone compile unit into " + defaultOutputPath,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			digestPath := ""
			if update {
				digestPath = digestFile
			}
			report, err := slice(source, out, digestPath)
			if err != nil {
				return err
			}
			if !quiet {
				fmt.Fprint(cmd.OutOrStdout(), report)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", defaultSourcePath, "directory of decompiled C#, one file per type under its namespace")
	cmd.Flags().StringVar(&out, "out", defaultOutputPath, "directory to write the compile unit into")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress the summary of what was sliced")
	cmd.Flags().BoolVar(&update, "update-digest", false,
		"rewrite "+digestFile+" from this run instead of holding the decompile to it")
	return cmd
}
