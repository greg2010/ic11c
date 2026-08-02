package oracle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// ErrNotBuilt reports that a harness is absent from this machine. It is not a test failure:
// neither harness is checked in, both need a network fetch to build, and CI may have neither a
// Rust toolchain nor Node. Shared turns it into a skip.
var ErrNotBuilt = errors.New("oracle harness is not built")

// Environment variables that override harness discovery.
const (
	// EnvIC10Emu points at a prebuilt ic10oracle executable.
	EnvIC10Emu = "IC11C_ORACLE_IC10EMU"
	// EnvNPM points at the oracle.cjs entry point of a staged npm harness.
	EnvNPM = "IC11C_ORACLE_NPM"
	// EnvRequired, set to any non-empty value, turns an absent harness from a skip into a
	// failure.
	//
	// A skip is otherwise invisible: `go test` discards a passing package's output unless -v is
	// given, so a corpus that compared nothing reads exactly like one that compared everything.
	// The Taskfile sets this for every target whose purpose is differential comparison, which is
	// what CI runs, so a job cannot report success having compared nothing.
	EnvRequired = "IC11C_ORACLE_REQUIRED"
)

// toolsDir is tools/oracle in the source tree that this package was compiled from. Tests run
// against a checkout, so it resolves; when it does not, the env overrides above still work.
var toolsDir = func() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "tools", "oracle")
}()

// notBuiltError names what is missing and how to produce it.
type notBuiltError struct {
	harness Harness
	missing string
	remedy  string
}

func (e *notBuiltError) Error() string {
	return fmt.Sprintf("%s: %s: %s is missing; %s", ErrNotBuilt, e.harness, e.missing, e.remedy)
}

func (e *notBuiltError) Unwrap() error { return ErrNotBuilt }

// Locate returns the argv that starts the given harness.
//
// Discovery order is the harness's environment override, then the default build output under
// tools/oracle. Every prerequisite is checked here so a missing one is reported by name rather
// than as an exec failure later.
func Locate(h Harness) ([]string, error) {
	switch h {
	case IC10Emu:
		return locateIC10Emu()
	case NPM:
		return locateNPM()
	default:
		return nil, fmt.Errorf("unknown harness %q", h)
	}
}

func locateIC10Emu() ([]string, error) {
	binary := os.Getenv(EnvIC10Emu)
	if binary == "" {
		if toolsDir == "" {
			return nil, &notBuiltError{IC10Emu, "source tree", "set " + EnvIC10Emu}
		}
		binary = filepath.Join(toolsDir, ".build", "release", "ic10oracle")
	}
	if _, err := os.Stat(binary); err != nil {
		return nil, &notBuiltError{IC10Emu, binary,
			"run tools/oracle/build-ic10emu.sh (needs cargo, and network on first run), or set " + EnvIC10Emu}
	}
	return []string{binary}, nil
}

func locateNPM() ([]string, error) {
	entry := os.Getenv(EnvNPM)
	if entry == "" {
		if toolsDir == "" {
			return nil, &notBuiltError{NPM, "source tree", "set " + EnvNPM}
		}
		entry = filepath.Join(toolsDir, "npm", "oracle.cjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, &notBuiltError{NPM, "node on PATH", "install Node, or skip the npm harness"}
	}
	for _, needed := range []string{entry, filepath.Join(filepath.Dir(entry), "ic10-bundle.cjs")} {
		if _, err := os.Stat(needed); err != nil {
			return nil, &notBuiltError{NPM, needed,
				"run tools/oracle/build-npm.sh (needs npm, and network on first run), or set " + EnvNPM}
		}
	}
	return []string{node, entry}, nil
}

// shared caches one client per harness for the lifetime of a test binary.
var (
	sharedMu sync.Mutex
	shared   = map[Harness]*sharedClient{}
)

type sharedClient struct {
	once   sync.Once
	client *Client
	err    error
}

func sharedEntry(h Harness) *sharedClient {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	sc, ok := shared[h]
	if !ok {
		sc = &sharedClient{}
		shared[h] = sc
	}
	return sc
}

// Shared returns a process-wide client for the harness, starting it on first use.
//
// It skips the calling test when the harness is not built on this machine, unless EnvRequired is
// set, and fails the test for any other startup problem. The skip is what lets the ordinary suite
// run on a machine with no Rust toolchain and no Node; EnvRequired is what stops that leniency
// from reaching a run whose whole purpose is differential comparison.
//
// The server is not closed: it exits by itself when the test binary does and the pipe closes.
// Callers wanting deterministic shutdown should use Start and Close.
func Shared(tb testing.TB, h Harness) *Client {
	tb.Helper()
	sc := sharedEntry(h)
	sc.once.Do(func() {
		sc.client, sc.err = Start(context.Background(), h)
	})
	switch {
	case errors.Is(sc.err, ErrNotBuilt):
		if os.Getenv(EnvRequired) != "" {
			tb.Fatalf("%s is set, so an absent harness is a failure rather than a skip: %v",
				EnvRequired, sc.err)
		}
		tb.Skipf("skipping: %v", sc.err)
	case sc.err != nil:
		tb.Fatalf("start %s oracle: %v", h, sc.err)
	}
	return sc.client
}
