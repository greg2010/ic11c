package difftest

import (
	"context"
	"fmt"

	"github.com/greg2010/ic11c/internal/chip"
)

// Run executes a generated program on the game's own chip. Taking a [Program]
// rather than source and state as separate arguments keeps a caller from
// pairing one program's source with another's initial state.
//
// The returned error is reserved for what is not a chip verdict; see
// [chip.Harness.Run]. A fault and a program that did not compile are both
// observations. harness must stay the faithful concrete type; see the
// assertion below.
func Run(ctx context.Context, harness *chip.Harness, p Program) (chip.Observation, error) {
	got, err := harness.Run(ctx, chip.Request{Source: p.Source, Initial: p.Initial})
	if err != nil {
		return chip.Observation{}, fmt.Errorf("running %s: %w", p, err)
	}
	return got, nil
}

// Run is pinned to *chip.Harness rather than an interface: every fault recipe
// here rests on the game's own devices (an unset pin raises DeviceNotFound
// only because nothing is on it), and a permissive process could answer
// something else. Pinning the concrete type makes that a compile error.
var _ func(context.Context, *chip.Harness, Program) (chip.Observation, error) = Run
