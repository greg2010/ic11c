package sema_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
)

// TestDeviceCodeRoundTrips holds the encoding an intrinsic call carries a
// device in. Instruction selection reads the pin back out of one integer
// constant, so a code that decodes to a different device is a program
// addressing the wrong thing with nothing to say so.
func TestDeviceCodeRoundTrips(t *testing.T) {
	tests := []struct {
		name   string
		device sema.Device
	}{
		{name: "the housing", device: sema.BaseDevice()},
		{name: "the first pin", device: sema.PinDevice(0)},
		{name: "the last pin", device: sema.PinDevice(5)},
		{name: "a pin the housing does not have", device: sema.PinDevice(9)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sema.DecodeDevice(tt.device.Code())
			if !ok {
				t.Fatalf("%s encodes to %d, which decodes to no device", tt.device, tt.device.Code())
			}
			if got != tt.device {
				t.Errorf("%s round tripped as %+v, want %+v", tt.device, got, tt.device)
			}
		})
	}
}

// TestDecodeDeviceRefusesWhatNoDeviceEncodes checks the other direction. Every
// device code comes from analysis, so a value outside the encoding is a defect
// in this compiler rather than in a program, and answering with some device
// anyway would put a line on the chip addressing one nobody named.
func TestDecodeDeviceRefusesWhatNoDeviceEncodes(t *testing.T) {
	tests := []struct {
		name string
		code int64
	}{
		{name: "below the housing", code: -2},
		{name: "far below the housing", code: -1 << 40},
		{name: "past the widest pin a byte holds", code: 256},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := sema.DecodeDevice(tt.code); ok {
				t.Errorf("the code %d decoded to %+v, want a refusal", tt.code, got)
			}
		})
	}
}

// TestDeviceString renders each spelling the way the source writes it, which is
// what a diagnostic naming a device quotes.
func TestDeviceString(t *testing.T) {
	tests := []struct {
		device sema.Device
		want   string
	}{
		{device: sema.BaseDevice(), want: "db"},
		{device: sema.PinDevice(0), want: "d0"},
		{device: sema.PinDevice(2), want: "d2"},
		{device: sema.PinDevice(5), want: "d5"},
	}
	for _, tt := range tests {
		if got := tt.device.String(); got != tt.want {
			t.Errorf("%+v renders as %q, want %q", tt.device, got, tt.want)
		}
	}
}

// TestTheHousingsPinsComeFromTheMachine holds what a device position resolves
// to the pin count internal/ic10 declares, in both directions and in the
// sentence the refusal is written with.
//
// A pin the housing does not have assembles on the chip and then faults once
// per tick with no error naming it, so a bound that drifted past the machine's
// would ship a program that does nothing and says nothing. A bound that drifted
// short would refuse a pin the housing has.
func TestTheHousingsPinsComeFromTheMachine(t *testing.T) {
	last := ic10.NumDevicePins - 1
	tests := []struct {
		name string
		pin  int
		// want are the fragments the refusal has to name, and is empty for a
		// pin the housing has.
		want []string
	}{
		{name: "the first pin", pin: 0},
		{name: "the last pin the housing has", pin: last},
		{
			name: "the first pin past the housing",
			pin:  last + 1,
			want: []string{
				fmt.Sprintf("'d%d' is not a device", last+1),
				fmt.Sprintf("db, d0 through d%d, or a dev object", last),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := fmt.Sprintf("const dev pin = d%d;\nvoid main(void) { __ic_sleep(1); }\n", tt.pin)
			_, diags := analyze(t, src)
			if len(tt.want) == 0 {
				if len(diags) != 0 {
					t.Fatalf("d%d was refused:\n%s", tt.pin, diags.String())
				}
				return
			}
			if len(diags) != 1 {
				t.Fatalf("d%d drew %d diagnostics, want one:\n%s", tt.pin, len(diags), diags.String())
			}
			for _, fragment := range tt.want {
				if !strings.Contains(diags[0].Msg, fragment) {
					t.Errorf("d%d drew %q, which does not name %q", tt.pin, diags[0].Msg, fragment)
				}
			}
		})
	}
}
