package main

const version = "0.6.0"

// Changelog:
// 0.1.0 - Switch panel support via hidapi (IOKit on macOS) + kRPC bridge
// 0.2.0 - Radio panel support with telemetry displays (4x5 digits)
// 0.3.0 - Both panels optional; initial switch state read on startup
// 0.4.0 - Navball speed (surface/orbital m/s); fixed body reference frame
// 0.5.0 - NAV1: speed in knots (navball); version printed on startup
// 0.6.0 - Altitude in km (1 decimal) to handle >99km without overflow
