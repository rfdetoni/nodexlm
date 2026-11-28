package protocol

import "testing"

func TestComputeAdmissionReason(t *testing.T) {
	hot, cool, low, normal := 3, 2, 15, 16
	charging, discharging := true, false
	for _, tt := range []struct {
		name string
		d    Device
		want string
	}{
		{"unknown telemetry", Device{}, ""},
		{"hot", Device{ThermalStatus: &hot}, "thermal_limit"},
		{"cool", Device{ThermalStatus: &cool}, ""},
		{"low battery", Device{BatteryPercent: &low, Charging: &discharging}, "battery_limit"},
		{"charging", Device{BatteryPercent: &low, Charging: &charging}, ""},
		{"unknown charging", Device{BatteryPercent: &low}, ""},
		{"recovered", Device{BatteryPercent: &normal, Charging: &discharging}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeAdmissionReason(tt.d); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
