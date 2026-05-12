package main

import "ksp-switchpanel/internal/version"

// version is the single source of truth; defined in internal/version/version.go.
const appVersion = version.Version

// Changelog:
// 0.1.0 - Switch panel support via hidapi (IOKit on macOS) + kRPC bridge
// 0.2.0 - Radio panel support with telemetry displays (4x5 digits)
// 0.3.0 - Both panels optional; initial switch state read on startup
// 0.4.0 - Navball speed (surface/orbital m/s); fixed body reference frame
// 0.5.0 - NAV1: speed in knots (navball); version printed on startup
// 0.6.0 - Altitude in km (1 decimal) to handle >99km without overflow
// 0.7.0 - Switch actions and radio display modes configurable via panels.toml
// 0.8.0 - Auto-reconnect kRPC and re-acquire vessel between missions
// 0.9.0 - GUI binary (ksp-switchpanel-ui) with status window; kRPC retry 1s
// 1.0.0 - Rotary → SAS mode; MechJeb AP integration; switch memory; panels blank on disconnect
// 1.1.0 - Multi panel (Flight Multi Panel) support: AP/HDG/ALT/VS/IAS holds + encoder + display
// 1.2.0 - Hot-plug: starts without any panel connected; panels detected/recovered automatically
// 1.2.1 - Fix HDG hold: enable RollHold alongside HeadingHold so MechJeb actually banks the plane
// 1.2.2 - AT=OFF mode: multi panel buttons select SmartASS SAS modes instead of airplane AP
// 1.2.3 - Radio panel shows clock (DD.MM / YYYY / HH.MM / SS) while kRPC is disconnected
// 1.2.4 - Fix action group indexing: kRPC is 0-based so config 1-10 now maps to KSP AG1-AG10
// 1.2.5 - SmartASS mode buttons enable SAS; toggling off disables SAS
