package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/ic10"
)

// headerPath is where prelude puts the header under a target directory: out of
// the way of the sources, which is the whole reason it is not written flat.
func headerPath(dir string) string {
	return filepath.Join(dir, ic10.PreludeDirName, ic10.PreludeFileName)
}

// wantFlags is the argument file prelude has to write, naming the header by the
// path a C driver resolves from the flags file's own directory.
func wantFlags(t *testing.T) string {
	t.Helper()
	flags, err := ic10.CompileFlagsIncluding(ic10.PreludeDirName + "/" + ic10.PreludeFileName)
	if err != nil {
		t.Fatalf("CompileFlagsIncluding: %v", err)
	}
	return flags
}

func TestPreludeWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := run(t, "prelude", dir)
	if err != nil {
		t.Fatalf("prelude %s: %v", dir, err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "header", path: headerPath(dir), want: ic10.Prelude},
		{name: "flags", path: filepath.Join(dir, ic10.CompileFlagsFileName), want: wantFlags(t)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("reading %s: %v", tt.path, err)
			}
			if string(got) != tt.want {
				t.Errorf("%s is not what the compiler carries", tt.path)
			}
			if !strings.Contains(stdout, tt.path) {
				t.Errorf("stdout does not name %s:\n%s", tt.path, stdout)
			}
		})
	}
}

// TestPreludeFlagsReachTheHeader resolves the emitted -include argument the way
// a C driver does, against the directory holding the flags file. An argument
// that only reads correctly from somewhere else is what this catches.
func TestPreludeFlagsReachTheHeader(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, "prelude", dir); err != nil {
		t.Fatalf("prelude %s: %v", dir, err)
	}

	path := filepath.Join(dir, ic10.CompileFlagsFileName)
	flags, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(flags), "\n"), "\n")
	if len(lines) < 2 || lines[len(lines)-2] != "-include" {
		t.Fatalf("%s does not end with -include and its argument on separate lines: %q", path, lines)
	}

	include := lines[len(lines)-1]
	resolved := filepath.Join(dir, filepath.FromSlash(include))
	if _, err := os.Stat(resolved); err != nil {
		t.Errorf("%s includes %s, which does not resolve from %s, the directory a C driver resolves it against: %v",
			path, include, dir, err)
	}
}

// TestPreludeRemovesAFlatHeader covers a directory a flat layout configured.
// The flags file names only the header under the generated directory, so a
// copy beside the sources is one nothing reads and nothing rewrites. Only a
// header recognised by the line every generated prelude opens with is removed.
func TestPreludeRemovesAFlatHeader(t *testing.T) {
	tests := []struct {
		name string
		// build puts the file of the header's name in dir. A case that only
		// needs contents leaves it nil.
		build    func(t *testing.T, flat string)
		contents string
		removed  bool
	}{
		{
			name:     "a header an older layout of this command left",
			contents: generatedHeaderMarker() + "\n// extracted from some earlier game version\n",
			removed:  true,
		},
		{
			name:     "a file of the same name this command did not write",
			contents: "// a header the author of this source tree wrote\n",
		},
		{
			name:     "a file shorter than the line every generated header opens with",
			contents: generatedHeaderMarker()[:8],
		},
		{
			// The link is the caller's and its target is somewhere nothing here
			// named, so following the name to decide and then unlinking the name
			// would take away what the caller made and leave what it pointed at.
			name: "a link of that name pointing at a header this command wrote",
			build: func(t *testing.T, flat string) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "elsewhere.h")
				if err := os.WriteFile(target, []byte(ic10.Prelude), 0o644); err != nil {
					t.Fatalf("writing %s: %v", target, err)
				}
				if err := os.Symlink(target, flat); err != nil {
					t.Fatalf("linking %s: %v", flat, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			flat := filepath.Join(dir, ic10.PreludeFileName)
			switch {
			case tt.build != nil:
				tt.build(t, flat)
			default:
				if err := os.WriteFile(flat, []byte(tt.contents), 0o644); err != nil {
					t.Fatalf("writing %s: %v", flat, err)
				}
			}

			_, stderr, err := run(t, "prelude", dir)
			if err != nil {
				t.Fatalf("prelude %s: %v", dir, err)
			}
			if _, err := os.Stat(headerPath(dir)); err != nil {
				t.Errorf("stat %s: %v", headerPath(dir), err)
			}
			if !strings.Contains(stderr, flat) {
				t.Errorf("stderr does not name %s, so nothing says what became of it:\n%s", flat, stderr)
			}

			if tt.removed {
				if _, err := os.Lstat(flat); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("stat %s = %v, want it gone", flat, err)
				}
				return
			}
			info, err := os.Lstat(flat)
			if err != nil {
				t.Fatalf("stat %s: %v", flat, err)
			}
			if tt.build != nil {
				if info.Mode()&fs.ModeSymlink == 0 {
					t.Errorf("%s is no longer a link, and this command did not make it one", flat)
				}
				return
			}
			got, err := os.ReadFile(flat)
			if err != nil {
				t.Fatalf("reading %s: %v", flat, err)
			}
			if string(got) != tt.contents {
				t.Errorf("%s was rewritten, and this command did not write it in the first place", flat)
			}
		})
	}
}

// commandDeadline is how long a run in this file is given to answer. A read
// or write that never finishes hangs the whole test binary rather than
// failing, so this turns that into a report; a prelude run costs no
// measurable time, so this stands thousands of times above it.
const commandDeadline = 5 * time.Second

// answered drives the command with a deadline and both streams captured. A
// goroutine still blocked when the deadline passes is left where it is: it
// is waiting on the very thing the case is about and holds nothing the rest
// of the run needs. A panic in it is reported rather than ending the binary.
func answered(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	type answer struct {
		stdout, stderr string
		err            error
	}
	done := make(chan answer, 1)
	// See [run] for why a nil argument list is replaced with an empty one.
	if args == nil {
		args = []string{}
	}
	go func() {
		var out, errOut bytes.Buffer
		cmd := rootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs(args)
		defer func() {
			if r := recover(); r != nil {
				done <- answer{out.String(), errOut.String(), fmt.Errorf("panicked: %v\n%s", r, debug.Stack())}
			}
		}()
		// Run first, then read the streams: the arguments of a send are
		// evaluated before it, so a call among them would be read from before it
		// had written anything.
		err := cmd.Execute()
		done <- answer{out.String(), errOut.String(), err}
	}()
	select {
	case got := <-done:
		return got.stdout, got.stderr, got.err
	case <-time.After(commandDeadline):
		t.Fatalf("ic11c %s did not answer within %s", strings.Join(args, " "), commandDeadline)
		return "", "", nil
	}
}

// TestPreludeDoesNotWaitOnAFileOfItsOwnName holds the subcommand to
// answering about a name in the caller's directory rather than growing or
// waiting on it: any of the three files it touches can already be a pipe or
// character device, where an ordinary read or write need never return.
func TestPreludeDoesNotWaitOnAFileOfItsOwnName(t *testing.T) {
	tests := []struct {
		name string
		// build puts the awkward name in dir.
		build func(t *testing.T, dir string) string
		want  int
	}{
		{
			name: "a pipe where the flat header goes, which a read waits on",
			build: func(t *testing.T, dir string) string {
				t.Helper()
				return mkfifo(t, filepath.Join(dir, ic10.PreludeFileName))
			},
			want: exitOK,
		},
		{
			name: "a link where the flat header goes, onto a device that answers forever",
			build: func(t *testing.T, dir string) string {
				t.Helper()
				const endless = "/dev/zero"
				if _, err := os.Stat(endless); err != nil {
					t.Skipf("%s is not there to read from: %v", endless, err)
				}
				path := filepath.Join(dir, ic10.PreludeFileName)
				if err := os.Symlink(endless, path); err != nil {
					t.Fatalf("linking %s: %v", path, err)
				}
				return path
			},
			want: exitOK,
		},
		{
			name: "a pipe where the flags file goes, which a write waits on",
			build: func(t *testing.T, dir string) string {
				t.Helper()
				return mkfifo(t, filepath.Join(dir, ic10.CompileFlagsFileName))
			},
			want: exitFailure,
		},
		{
			name: "a pipe where the header goes, which a write waits on",
			build: func(t *testing.T, dir string) string {
				t.Helper()
				headerDir := filepath.Join(dir, ic10.PreludeDirName)
				if err := os.Mkdir(headerDir, 0o755); err != nil {
					t.Fatalf("creating %s: %v", headerDir, err)
				}
				return mkfifo(t, filepath.Join(headerDir, ic10.PreludeFileName))
			},
			want: exitFailure,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tt.build(t, dir)
			stdout, stderr, err := answered(t, "prelude", dir)
			if got := exitCodeFor(err); got != tt.want {
				t.Errorf("exit status %d, want %d: %v\n%s", got, tt.want, err, stderr)
			}
			if err != nil && !strings.Contains(err.Error(), path) {
				t.Errorf("the message does not name %s, which is the only thing telling this run from the one the caller meant: %v", path, err)
			}
			if tt.want != exitOK {
				// The two files only configure an editor together, so a run that
				// could not put both down produced nothing to name.
				if stdout != "" {
					t.Errorf("the output stream names %q for a run that failed, and a caller reads that as a file it can use", stdout)
				}
				return
			}
			if !strings.Contains(stderr, path) {
				t.Errorf("stderr does not name %s, so nothing says it was left alone:\n%s", path, stderr)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Errorf("stat %s = %v, want it left where it was", path, err)
			}
		})
	}
}

// mkfifo makes a named pipe with no writer and no reader, which is the shape an
// ordinary open waits on in either direction.
func mkfifo(t *testing.T, path string) string {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("making a pipe at %s: %v", path, err)
	}
	return path
}

// TestPreludeRewritesAnExistingLayout covers a second run against a
// directory already configured, where each file can be longer than what
// replaces it: the header shrinks whenever the game drops a name, so a run
// leaving an old tail behind would leave a header nothing wrote and a C editor reads.
func TestPreludeRewritesAnExistingLayout(t *testing.T) {
	tests := []struct {
		name string
		// build puts what the run has to replace in dir.
		build func(t *testing.T, dir string)
	}{
		{
			name: "a directory an earlier run configured",
			build: func(t *testing.T, dir string) {
				t.Helper()
				if _, _, err := run(t, "prelude", dir); err != nil {
					t.Fatalf("prelude %s: %v", dir, err)
				}
			},
		},
		{
			name: "files longer than the ones that replace them",
			build: func(t *testing.T, dir string) {
				t.Helper()
				headerDir := filepath.Join(dir, ic10.PreludeDirName)
				if err := os.Mkdir(headerDir, 0o755); err != nil {
					t.Fatalf("creating %s: %v", headerDir, err)
				}
				const tail = "\n// a declaration the game has since dropped\n"
				for _, file := range preludeLayout(t, dir) {
					if err := os.WriteFile(file.path, []byte(file.contents+tail), 0o644); err != nil {
						t.Fatalf("writing %s: %v", file.path, err)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.build(t, dir)
			if _, _, err := run(t, "prelude", dir); err != nil {
				t.Fatalf("prelude %s: %v", dir, err)
			}
			for _, file := range preludeLayout(t, dir) {
				got, err := os.ReadFile(file.path)
				if err != nil {
					t.Fatalf("reading %s: %v", file.path, err)
				}
				if string(got) != file.contents {
					t.Errorf("%s is %d bytes and what the compiler carries is %d, so what was there before it was not all replaced",
						file.path, len(got), len(file.contents))
				}
			}
		})
	}
}

// preludeLayout is the pair of files a configured directory holds and what each
// has to say.
func preludeLayout(t *testing.T, dir string) []struct{ path, contents string } {
	t.Helper()
	return []struct{ path, contents string }{
		{path: headerPath(dir), contents: ic10.Prelude},
		{path: filepath.Join(dir, ic10.CompileFlagsFileName), contents: wantFlags(t)},
	}
}

// fileKind is a description carrying nothing but the kind of file it is, which
// is all [readOpening] asks of one.
type fileKind fs.FileMode

func (k fileKind) Name() string       { return ic10.PreludeFileName }
func (k fileKind) Size() int64        { return 0 }
func (k fileKind) Mode() fs.FileMode  { return fs.FileMode(k) }
func (k fileKind) ModTime() time.Time { return time.Time{} }
func (k fileKind) IsDir() bool        { return fs.FileMode(k).IsDir() }
func (k fileKind) Sys() any           { return nil }

// describedFile is an open file whose description and whose contents a case
// states separately. A name on disk cannot do that — describing it and reading
// it are one resolution there — and separating them is what makes it visible
// which of the two a decision was made from.
type describedFile struct {
	described fs.FileInfo
	statErr   error
	read      io.Reader
}

func (f *describedFile) Stat() (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.described, nil
}

func (f *describedFile) Read(p []byte) (int, error) { return f.read.Read(p) }

func (f *describedFile) Close() error { return nil }

// TestReadOpeningIsDescribedByTheHandle holds the decision about a file of
// the header's name to the handle that was opened and to the opening line
// alone. The path each case passes names nothing on disk, so a description
// taken from the name rather than the handle fails outright.
func TestReadOpeningIsDescribedByTheHandle(t *testing.T) {
	marker := generatedHeaderMarker()
	tests := []struct {
		name string
		file describedFile
		// want is the opening that has to come back, and says what a refusal has
		// to carry instead. At most one of the two is set.
		want string
		says string
	}{
		{
			name: "a header this command wrote",
			file: describedFile{described: fileKind(0o644), read: strings.NewReader(ic10.Prelude)},
			want: marker,
		},
		{
			name: "a file shorter than the line every generated header opens with",
			file: describedFile{described: fileKind(0o644), read: strings.NewReader(marker[:8])},
			want: marker[:8],
		},
		{
			// The reader fails on any read past the opening line, which is what
			// a read bounded by the file's own end reaches. A file of this name
			// in the caller's directory can be as large as they like, and only
			// the opening line decides.
			name: "a file with more to give than the decision needs",
			file: describedFile{
				described: fileKind(0o644),
				read:      io.MultiReader(strings.NewReader(marker), refusingReader{}),
			},
			want: marker,
		},
		{
			// The name says a plain file and the handle says otherwise, which is
			// what the window between examining a name and opening it reaches.
			// Reading such a handle is what need never finish.
			name: "a handle describing a pipe, whatever it would answer with",
			file: describedFile{described: fileKind(fs.ModeNamedPipe), read: strings.NewReader(ic10.Prelude)},
		},
		{
			name: "a handle describing a directory, whatever it would answer with",
			file: describedFile{described: fileKind(fs.ModeDir), read: strings.NewReader(ic10.Prelude)},
		},
		{
			name: "a handle that cannot describe itself",
			file: describedFile{statErr: errors.New(refusingReaderMsg)},
			says: refusingReaderMsg,
		},
		{
			name: "a read that fails before the line is out",
			file: describedFile{described: fileKind(0o644), read: refusingReader{}},
			says: refusingReaderMsg,
		},
	}
	path := filepath.Join(t.TempDir(), ic10.PreludeFileName)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readOpening(path, &tt.file)
			if tt.says != "" {
				if err == nil {
					t.Fatalf("the opening came back as %q and nothing was reported", got)
				}
				if !strings.Contains(err.Error(), path) {
					t.Errorf("the message does not name %s, which is the only thing telling this run from the one the caller meant: %v", path, err)
				}
				if !strings.Contains(err.Error(), tt.says) {
					t.Errorf("the message does not say %q, so it does not say what about the file failed: %v", tt.says, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("reading the opening of %s: %v", path, err)
			}
			if got != tt.want {
				t.Errorf("the opening came back as %q, want %q", got, tt.want)
			}
		})
	}
}

// haltingFile takes a stated number of bytes fewer than it is given,
// reports what a case says of the write, and reports its own failure when
// closed. It stands in for the two failures a name on disk will not produce
// to order: a write stopping part way, and a close reporting bytes that never landed.
type haltingFile struct {
	short    int
	writeErr error
	closeErr error
	written  strings.Builder
}

func (f *haltingFile) Write(p []byte) (int, error) {
	n := max(len(p)-f.short, 0)
	f.written.Write(p[:n])
	return n, f.writeErr
}

func (f *haltingFile) Close() error { return f.closeErr }

// TestWriteThroughReportsAWriteThatDidNotFinish holds each file prelude
// writes to landing whole. The two files only configure an editor together
// and a C editor reads whatever is under the name, so a header that stopped
// part way is one clangd parses and reports the caller's own source against.
func TestWriteThroughReportsAWriteThatDidNotFinish(t *testing.T) {
	const contents = "// a header some run of this command wrote\n"
	const stopped = "the filesystem stopped taking bytes"
	tests := []struct {
		name string
		file haltingFile
		// says is what the message has to carry beyond the path. A case that has
		// to succeed leaves it empty.
		says []string
	}{
		{name: "a file that takes everything and closes"},
		{
			name: "a write that fails",
			file: haltingFile{writeErr: errors.New(stopped)},
			says: []string{"writing", stopped},
		},
		{
			name: "a write that takes all but the last byte and says it took everything",
			file: haltingFile{short: 1},
			says: []string{"writing", io.ErrShortWrite.Error()},
		},
		{
			name: "a close that says the bytes never landed",
			file: haltingFile{closeErr: errors.New(stopped)},
			says: []string{"closing", stopped},
		},
		{
			name: "a write and a close that both fail",
			file: haltingFile{writeErr: errors.New(stopped), closeErr: errors.New(stopped)},
			says: []string{"writing", "closing"},
		},
	}
	path := filepath.Join(t.TempDir(), ic10.PreludeFileName)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writeThrough(path, &tt.file, contents)
			if len(tt.says) == 0 {
				if err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}
				if got := tt.file.written.String(); got != contents {
					t.Errorf("the file holds %q, want %q", got, contents)
				}
				return
			}
			if err == nil {
				t.Fatalf("the write was accepted, and a file that did not take all of it is what has to be reported")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the message does not name %s, so nothing says which file did not land: %v", path, err)
			}
			for _, says := range tt.says {
				if !strings.Contains(err.Error(), says) {
					t.Errorf("the message does not say %q: %v", says, err)
				}
			}
		})
	}
}

// TestPreludeDefaultsToTheWorkingDirectory covers the bootstrapping case, where
// a user runs it from the script repository it is meant to configure.
func TestPreludeDefaultsToTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, _, err := run(t, "prelude"); err != nil {
		t.Fatalf("prelude: %v", err)
	}
	for _, path := range []string{headerPath(dir), filepath.Join(dir, ic10.CompileFlagsFileName)} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("stat %s: %v", path, err)
		}
	}
}

// TestPreludeErrors holds every way the subcommand fails to a status and to
// an empty output stream: exitFailure is a name that could not be read or
// written, exitUsage the command line itself, exitInternal a binary defect —
// and a mistyped directory reported as either of the last two would mislead.
func TestPreludeErrors(t *testing.T) {
	tests := []struct {
		name string
		// build prepares the directory the case runs against and returns the
		// arguments. A case with nothing to prepare returns args alone.
		build   func(t *testing.T) []string
		want    int
		wantErr string
	}{
		{
			name: "directory does not exist",
			build: func(t *testing.T) []string {
				t.Helper()
				return []string{"prelude", filepath.Join(t.TempDir(), "absent")}
			},
			want:    exitFailure,
			wantErr: ic10.PreludeDirName,
		},
		{
			name:    "more than one directory",
			build:   func(*testing.T) []string { return []string{"prelude", ".", ".."} },
			want:    exitUsage,
			wantErr: "accepts at most 1 arg",
		},
		{
			// The second of the two files cannot be written, so the failure
			// arrives after the first one already landed.
			name: "the flags file cannot be written",
			build: func(t *testing.T) []string {
				t.Helper()
				dir := t.TempDir()
				blocked := filepath.Join(dir, ic10.CompileFlagsFileName)
				if err := os.Mkdir(blocked, 0o755); err != nil {
					t.Fatalf("blocking %s: %v", blocked, err)
				}
				return []string{"prelude", dir}
			},
			want:    exitFailure,
			wantErr: ic10.CompileFlagsFileName,
		},
		{
			// Both files land and the run fails on the one step after them, which
			// is where a list already on the output stream would be a list of
			// files a failing run says it produced.
			name: "a file of the header's name beside the sources that cannot be read",
			build: func(t *testing.T) []string {
				t.Helper()
				if os.Geteuid() == 0 {
					t.Skip("a file with no permissions is readable by root, so nothing here fails")
				}
				dir := t.TempDir()
				flat := filepath.Join(dir, ic10.PreludeFileName)
				if err := os.WriteFile(flat, []byte(ic10.Prelude), 0o000); err != nil {
					t.Fatalf("writing %s: %v", flat, err)
				}
				return []string{"prelude", dir}
			},
			want:    exitFailure,
			wantErr: ic10.PreludeFileName,
		},
		{
			// Both files are already there and writable, so the run gets past
			// writing them and fails only on removing the flat header from a
			// directory it may not write into — which still must fail, since
			// a header nothing names and no later run rewrites would otherwise remain.
			name: "a header this command wrote that cannot be removed",
			build: func(t *testing.T) []string {
				t.Helper()
				if os.Geteuid() == 0 {
					t.Skip("a directory nobody may write into is writable by root, so nothing here fails")
				}
				dir := t.TempDir()
				if _, _, err := run(t, "prelude", dir); err != nil {
					t.Fatalf("configuring %s: %v", dir, err)
				}
				flat := filepath.Join(dir, ic10.PreludeFileName)
				if err := os.WriteFile(flat, []byte(ic10.Prelude), 0o644); err != nil {
					t.Fatalf("writing %s: %v", flat, err)
				}
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatalf("sealing %s: %v", dir, err)
				}
				// What t.TempDir removes at the end of the run it removes by
				// writing into this directory.
				t.Cleanup(func() {
					if err := os.Chmod(dir, 0o755); err != nil {
						t.Errorf("unsealing %s: %v", dir, err)
					}
				})
				return []string{"prelude", dir}
			},
			want:    exitFailure,
			wantErr: "removing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.build(t)
			stdout, _, err := run(t, args...)
			if err == nil {
				t.Fatalf("run(%q) succeeded, want an error", args)
			}
			if got := exitCodeFor(err); got != tt.want {
				t.Errorf("exit status %d, want %d: %v", got, tt.want, err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
			// The output stream is the list of what was written and a script
			// reads it as what it got, so a failing run names nothing on it —
			// whatever it did or did not leave on disk.
			if stdout != "" {
				t.Errorf("the output stream names %q for a run that failed, and a caller reads that as a file it can use", stdout)
			}
		})
	}
}

// TestSubcommandDoesNotShadowASource pins the invocation the subcommand was
// added underneath: cobra falls through to the compiler for any first argument
// that is not a subcommand name.
func TestSubcommandDoesNotShadowASource(t *testing.T) {
	if len(compileFixture(t, "thermostat.c")) == 0 {
		t.Error("compiling a source emitted no assembly")
	}
}
