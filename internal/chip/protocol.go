package chip

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Command verbs. Every one of them replies with a single line beginning "ok" or
// "err", except state and fixture trace, which reply with a block ending in
// "end".
const (
	cmdReset   = "reset"
	cmdQuit    = "quit"
	cmdState   = "state"
	cmdSource  = "src"
	cmdReg     = "reg"
	cmdStack   = "stack"
	cmdClock   = "clock"
	cmdSeed    = "seed"
	cmdRun     = "run"
	cmdRunTo   = "runto"
	cmdDev     = "dev"
	cmdFixture = "fixture"
	cmdAddress = "ip"
	cmdGet     = "get"
	cmdGetSlot = "gets"
	cmdLimits  = "limits"
)

// okPrefix begins the harness's answer to every command that succeeded; a
// failure begins "err". Only the success is matched on: "err" is also the state
// block's key for the run time error, so a reply is judged by whether it says
// ok rather than by whether it says err.
const okPrefix = "ok"

// The two answers run gives. It says 1 when the segment ended by running out of
// the instructions it was given with the program still inside itself, and 0
// every other way a segment ends.
const (
	runExhausted = okPrefix + " 1"
	runStopped   = okPrefix + " 0"
)

// blockEnd terminates the state block and the fixture trace block.
const blockEnd = "end"

// segmentStops is what runto may name as the ending of its last tick.
// [StopTickBudget] is deliberately absent: how many ticks a run may spend is
// the caller's own bound, not something the harness can report, so accepting
// the word here would let an unchecked harness answer be repeated as fact.
var segmentStops = map[string]StopReason{
	string(StopEnded):        StopEnded,
	string(StopFaulted):      StopFaulted,
	string(StopCompileError): StopCompileError,
	string(StopSuspended):    StopSuspended,
	string(StopBudget):       StopBudget,
}

// parseRunTo reads runto's reply: how the last tick ended, and how many ticks
// ran. The tick count is checked against the limit the run asked for by
// [observation], which is where the limit is known.
func parseRunTo(line string) (StopReason, int, error) {
	answer, ok := strings.CutPrefix(line, okPrefix+" ")
	if !ok {
		return "", 0, fmt.Errorf("harness answered %q, want %q, a stop reason and a tick count",
			line, okPrefix)
	}
	fields := strings.Fields(answer)
	if len(fields) != 2 {
		return "", 0, fmt.Errorf("harness answered %q, want a stop reason and a tick count", line)
	}
	reason, ok := segmentStops[fields[0]]
	if !ok {
		return "", 0, fmt.Errorf("harness ended a tick %q, which is not an ending one tick has", fields[0])
	}
	ticks, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, fmt.Errorf("tick count %q: %w", fields[1], err)
	}
	return reason, ticks, nil
}

// The one spelling a double is written in. The width is fixed so that a
// truncated token is a refusal rather than a value sixteen times too small, and
// the prefix so that a bit pattern cannot be read as the decimal it happens to
// look like.
const (
	bitsPrefix = "0x"
	bitsDigits = 16
)

// encodeText carries an argument whose own spacing matters through a protocol
// that splits a command on spaces: a program's indentation and newlines, and a
// device's display name.
func encodeText(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}

// formatBits renders a double the way the harness reads one.
func formatBits(value float64) string {
	return bitsPrefix + fmt.Sprintf("%0*x", bitsDigits, math.Float64bits(value))
}

// parseBits accepts exactly what formatBits writes and nothing else. strconv's
// own parser would take an upper case token, a shorter one, or a sign, each of
// which would read as a value rather than the protocol error it is. Sixteen
// digits is exactly 64 bits, so an accepted token cannot overflow.
func parseBits(text string) (float64, error) {
	if len(text) != len(bitsPrefix)+bitsDigits || !strings.HasPrefix(text, bitsPrefix) {
		return 0, fmt.Errorf("a double is %q and %d lower case hexadecimal digits, got %q",
			bitsPrefix, bitsDigits, text)
	}
	digits := text[len(bitsPrefix):]
	for _, c := range []byte(digits) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return 0, fmt.Errorf("a double is %q and %d lower case hexadecimal digits, got %q",
				bitsPrefix, bitsDigits, text)
		}
	}
	bits, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q as a double: %w", text, err)
	}
	return math.Float64frombits(bits), nil
}

// Snapshot is the chip's state, as the harness's state block reports it.
type Snapshot struct {
	Registers [ic10.NumRegisters]float64
	// Stack is the chip's 512 slot array, which is both the push/pop stack and
	// the addressable data region.
	Stack [ic10.NumMemorySlots]float64
	// Address is the line the next segment would run.
	Address int
	// LineCount is how many lines compiled, which a compile error truncates.
	LineCount int
	// Fault is the run time error the chip last recorded. It is cleared by any
	// instruction that completes, so it is set only when the run stopped on it.
	Fault Fault
	// CompileError is the error that stopped compilation.
	CompileError Fault
	// HousingError is CircuitHousing._codeErrorState, 1 while the last
	// instruction raised.
	HousingError int
	// FixtureWrites is how many writes [FixtureHarness.Trace] would report. Only
	// a permissive process reports it; see the package doc.
	FixtureWrites int
}

// parseSnapshot reads the lines between "state" and its terminating "end".
// Every key is required, an unknown key is refused, and a repeated key is
// refused too — otherwise a growing or duplicated block would silently read as
// a state neither end agreed on. A stack line is keyed by its address, since
// one line exists per occupied slot. fixtures gates the "fixtures" key both
// ways, so neither end can be wrong about which kind of process this is
// without the read stopping.
func parseSnapshot(lines []string, fixtures bool) (Snapshot, error) {
	var snapshot Snapshot
	seen := make(map[string]bool, len(lines))

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return Snapshot{}, fmt.Errorf("blank line in a state block")
		}
		key, err := fields[0], error(nil)
		switch {
		case key == "regs":
			err = parseRegs(&snapshot, fields[1:])
		case key == "stack":
			var address int
			if address, err = parseStackSlot(&snapshot, fields[1:]); err == nil {
				key += " " + strconv.Itoa(address)
			}
		case key == "ip":
			err = parseInt(&snapshot.Address, fields[1:], key)
		case key == "lines":
			err = parseInt(&snapshot.LineCount, fields[1:], key)
		case key == "err":
			err = parseFault(&snapshot.Fault, fields[1:], key)
		case key == "cerr":
			err = parseFault(&snapshot.CompileError, fields[1:], key)
		case key == "housing":
			err = parseInt(&snapshot.HousingError, fields[1:], key)
		case key == "fixtures" && fixtures:
			err = parseInt(&snapshot.FixtureWrites, fields[1:], key)
		default:
			err = fmt.Errorf("unknown state key %q", key)
		}
		if err != nil {
			return Snapshot{}, err
		}
		if seen[key] {
			return Snapshot{}, fmt.Errorf("state block has two %q lines", key)
		}
		seen[key] = true
	}

	for _, key := range requiredStateKeys(fixtures) {
		if !seen[key] {
			return Snapshot{}, fmt.Errorf("state block has no %q line", key)
		}
	}
	return snapshot, nil
}

// requiredStateKeys is every key a state block must carry, one line each. It
// answers both what a block is incomplete without and how many lines it can be
// before it isn't one — writing those out twice would let them silently drift.
// The stack is not here: it carries one line per occupied slot, bounded by the
// array rather than this list.
func requiredStateKeys(fixtures bool) []string {
	keys := []string{"regs", "ip", "lines", "err", "cerr", "housing"}
	if fixtures {
		keys = append(keys, "fixtures")
	}
	return keys
}

func parseRegs(snapshot *Snapshot, values []string) error {
	if len(values) != ic10.NumRegisters {
		return fmt.Errorf("state block reported %d registers, want %d", len(values), ic10.NumRegisters)
	}
	for i, text := range values {
		value, err := parseBits(text)
		if err != nil {
			return fmt.Errorf("register %d: %w", i, err)
		}
		snapshot.Registers[i] = value
	}
	return nil
}

// parseStackSlot reads one slot line and returns the address it filled, which
// is what [parseSnapshot] keys the line by.
func parseStackSlot(snapshot *Snapshot, fields []string) (int, error) {
	if len(fields) != 2 {
		return 0, fmt.Errorf("stack line wants an address and a value, got %d fields", len(fields))
	}
	address, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("stack address %q: %w", fields[0], err)
	}
	if address < 0 || address >= ic10.NumMemorySlots {
		return 0, fmt.Errorf("stack address %d is outside the %d slot array", address, ic10.NumMemorySlots)
	}
	value, err := parseBits(fields[1])
	if err != nil {
		return 0, fmt.Errorf("stack slot %d: %w", address, err)
	}
	snapshot.Stack[address] = value
	return address, nil
}

func parseInt(dest *int, fields []string, key string) error {
	if len(fields) != 1 {
		return fmt.Errorf("%s line wants one field, got %d", key, len(fields))
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil {
		return fmt.Errorf("%s %q: %w", key, fields[0], err)
	}
	*dest = value
	return nil
}

func parseFault(dest *Fault, fields []string, key string) error {
	if len(fields) != 2 {
		return fmt.Errorf("%s line wants a type and a line, got %d fields", key, len(fields))
	}
	exception, err := parseExceptionType(fields[0])
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	line, err := strconv.Atoi(fields[1])
	if err != nil {
		return fmt.Errorf("%s line number %q: %w", key, fields[1], err)
	}
	*dest = Fault{Type: exception, Line: line}
	return nil
}
