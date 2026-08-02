package sema_test

import (
	"testing"

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
