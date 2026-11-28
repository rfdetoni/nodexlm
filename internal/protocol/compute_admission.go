package protocol

// ComputeAdmissionReason is a conservative policy for new work only. Unknown
// telemetry must not be mistaken for a hot or discharged device.
func ComputeAdmissionReason(d Device) string {
	if d.ThermalStatus != nil && *d.ThermalStatus >= 3 {
		return "thermal_limit"
	}
	if d.BatteryPercent != nil && d.Charging != nil && !*d.Charging && *d.BatteryPercent <= 15 {
		return "battery_limit"
	}
	return ""
}
