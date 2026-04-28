package bridge

// BridgeStatus is a snapshot of the connection state of all components.
// Sent on the status channel whenever something changes.
type BridgeStatus struct {
	SwitchPanel   bool    // Switch panel hardware detected
	RadioPanel    bool    // Radio panel hardware detected
	MultiPanel    bool    // Multi panel hardware detected
	KRPC          string  // "connecting" | "connected"
	VesselName    string  // Active vessel name; "" = no active vessel
	MechJeb       bool    // MechJeb airplane autopilot available
	APSpeedHold   bool    // Speed hold enabled
	APAltHold     bool    // Altitude hold enabled
	APSpeedTarget float64 // Speed hold target (m/s)
	APAltTarget   float64 // Altitude hold target (m)
}
