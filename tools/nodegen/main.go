// Command nodegen renders the tree-sitter C grammar's own description of
// itself into internal/tsnode, the Go tables internal/tsparse converts
// against. sync copies node-types.json and grammar.json into the tree;
// generate renders them, so a grammar update moves a checked-in file rather than a converter quietly reading something else.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Default locations of the checked-in artifacts, relative to the module
// root where the generator runs. The vendored grammar sits beside the Go
// rendered from it, in a directory holding no hand-written source, which
// is what scripts/check-codegen.sh holds it to.
const (
	defaultNodeTypesPath = "internal/tsnode/node-types.json"
	defaultGrammarPath   = "internal/tsnode/grammar.json"
	defaultKindsPath     = "internal/tsnode/nodekinds.gen.go"
)

// grammarModule is the Go module the grammar ships in, and the two paths are
// where it describes itself within that module. sync resolves the module through
// the go command rather than embedding the files, because go:embed cannot cross
// a module boundary.
const (
	grammarModule    = "github.com/tree-sitter/tree-sitter-c"
	grammarNodeTypes = "src/node-types.json"
	grammarRules     = "src/grammar.json"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "nodegen: %v\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "nodegen",
		Short: "Build the tree-sitter node kind tables",
		Long: `nodegen renders the tree-sitter C grammar's description of itself into Go.

sync copies ` + grammarNodeTypes + ` and ` + grammarRules + ` out of
` + grammarModule + ` into ` + defaultNodeTypesPath + ` and
` + defaultGrammarPath + `, and generate renders both into
` + defaultKindsPath + `. Every output is checked in.`,
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
	root.AddCommand(syncCmd(), generateCmd())
	return root
}

func syncCmd() *cobra.Command {
	var nodeTypes, rules string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Copy the grammar's description of itself into " + defaultNodeTypesPath + " and " + defaultGrammarPath,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return sync(nodeTypes, rules)
		},
	}
	cmd.Flags().StringVar(&nodeTypes, "out", defaultNodeTypesPath, "path of the grammar node types to write")
	cmd.Flags().StringVar(&rules, "grammar", defaultGrammarPath, "path of the grammar rules to write")
	return cmd
}

func generateCmd() *cobra.Command {
	var nodeTypes, rules, out string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Render " + defaultKindsPath + " from the checked-in grammar",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return generate(nodeTypes, rules, out)
		},
	}
	cmd.Flags().StringVar(&nodeTypes, "node-types", defaultNodeTypesPath, "path of the grammar node types to read")
	cmd.Flags().StringVar(&rules, "grammar", defaultGrammarPath, "path of the grammar rules to read")
	cmd.Flags().StringVar(&out, "kinds", defaultKindsPath, "path of the Go node kind tables to write")
	return cmd
}
