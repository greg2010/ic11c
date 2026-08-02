package vm

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

func TestHousingAttachRejectsAPinOutsideTheHousing(t *testing.T) {
	tests := []struct {
		name string
		pin  int
		want bool
	}{
		{name: "below the first pin", pin: -1},
		{name: "the first pin", pin: 0, want: true},
		{name: "the last pin", pin: ic10.NumDevicePins - 1, want: true},
		{name: "one past the last pin", pin: ic10.NumDevicePins},
		{name: "the base unit index is not a pin", pin: BaseUnitIndex},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHousing()
			if got := h.Attach(tt.pin, pump(101, 5)); got != tt.want {
				t.Errorf("Attach(%d) = %v, want %v", tt.pin, got, tt.want)
			}
		})
	}
}

// TestHousingAttach covers the three things one call changes together: the pin,
// the data network a pin reference is checked against, and the reference id
// index. Each row runs against the housing the rows before it left behind.
func TestHousingAttach(t *testing.T) {
	first, second, third := pump(101, 1), pump(102, 2), pump(103, 3)
	h := &Housing{}

	tests := []struct {
		name        string
		pin         int
		device      Device
		wantOnPin   Device
		wantByID    int
		wantDropped int
		wantNetwork int
	}{
		{
			name: "the zero value housing takes a device",
			pin:  0, device: first,
			wantOnPin: first, wantByID: 101, wantNetwork: 1,
		},
		{
			name: "a second pin joins the same network",
			pin:  1, device: second,
			wantOnPin: second, wantByID: 102, wantNetwork: 2,
		},
		{
			name: "replacing a device drops the one it replaced",
			pin:  0, device: third,
			wantOnPin: third, wantByID: 103, wantDropped: 101, wantNetwork: 2,
		},
		{
			name: "attaching nothing empties the pin",
			pin:  1, device: nil,
			wantOnPin: nil, wantDropped: 102, wantNetwork: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !h.Attach(tt.pin, tt.device) {
				t.Fatalf("Attach(%d) reported failure", tt.pin)
			}
			got, err := h.logicableFromIndex(tt.pin, BaseNetworkIndex)
			if err != nil {
				t.Fatalf("logicableFromIndex(%d): %v", tt.pin, err)
			}
			if got != tt.wantOnPin {
				t.Errorf("pin %d holds %v, want %v", tt.pin, got, tt.wantOnPin)
			}
			if tt.wantByID != 0 && h.logicableFromID(tt.wantByID, BaseNetworkIndex) == nil {
				t.Errorf("reference id %d resolves to nothing", tt.wantByID)
			}
			if tt.wantDropped != 0 && h.logicableFromID(tt.wantDropped, BaseNetworkIndex) != nil {
				t.Errorf("reference id %d still resolves after being replaced", tt.wantDropped)
			}
			if got := len(h.batchOutput()); got != tt.wantNetwork {
				t.Errorf("the data network holds %d devices, want %d", got, tt.wantNetwork)
			}
		})
	}
}

// TestNewHousingBoundsItsPins covers the two inputs NewHousing quietly absorbs
// rather than reporting: an empty pin, and more devices than there are pins.
func TestNewHousingBoundsItsPins(t *testing.T) {
	devices := make([]Device, 0, ic10.NumDevicePins+2)
	devices = append(devices, nil)
	for i := 1; i < ic10.NumDevicePins+2; i++ {
		devices = append(devices, pump(100+i, float64(i)))
	}
	h := NewHousing(devices...)

	tests := []struct {
		name     string
		pin      int
		wantPin  bool
		wantErr  bool
		wantByID int
	}{
		{name: "a nil device leaves the pin empty", pin: 0},
		{name: "the second device lands on d1", pin: 1, wantPin: true, wantByID: 101 + 1},
		{name: "the last pin is filled", pin: ic10.NumDevicePins - 1, wantPin: true},
		{name: "a pin past the housing is a host exception", pin: ic10.NumDevicePins, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := h.logicableFromIndex(tt.pin, BaseNetworkIndex)
			if (err != nil) != tt.wantErr {
				t.Fatalf("logicableFromIndex(%d) error = %v, want an error: %v", tt.pin, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if (got != nil) != tt.wantPin {
				t.Errorf("pin %d holds %v, want a device: %v", tt.pin, got, tt.wantPin)
			}
			if tt.wantByID != 0 && h.logicableFromID(tt.wantByID, BaseNetworkIndex) == nil {
				t.Errorf("reference id %d resolves to nothing", tt.wantByID)
			}
		})
	}
	// The devices past the last pin are dropped rather than renumbering it.
	if got := len(h.batchOutput()); got != ic10.NumDevicePins-1 {
		t.Errorf("the data network holds %d devices, want %d", got, ic10.NumDevicePins-1)
	}
}
