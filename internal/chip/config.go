package chip

import (
	"fmt"
	"os"
)

// Environment the driver is configured from. Neither setting has a default: a
// missing one is an error, since defaulting either would let a run silently
// answer questions about a chip nobody built.
const (
	// EnvEnabled must be set for a test that needs a running chip to run at all.
	// Nothing else gates one: once it is set, a chip that cannot be reached is a
	// failure and never a skip.
	EnvEnabled = "IC11C_CHIP"
	// EnvImage is the digest-pinned Mono image.
	EnvImage = "IC11C_CHIP_IMAGE"
	// EnvBinDir is the host directory task chip:build wrote chip.exe into.
	EnvBinDir = "IC11C_CHIP_BIN"
)

// Enabled reports whether a run against the real chip was asked for.
func Enabled() bool { return os.Getenv(EnvEnabled) != "" }

// EnvOptions reads the configuration from the environment. A missing setting is
// an error rather than a default; see the constants.
func EnvOptions() (Options, error) {
	image := os.Getenv(EnvImage)
	if image == "" {
		return Options{}, fmt.Errorf("%s is not set, so there is no image to run", EnvImage)
	}
	binDir := os.Getenv(EnvBinDir)
	if binDir == "" {
		return Options{}, fmt.Errorf("%s is not set, so there is no chip binary to run", EnvBinDir)
	}
	return Options{Image: image, BinDir: binDir}, nil
}
