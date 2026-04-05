# ksp-switchpanel

Connects **Logitech/Saitek Flight Switch Panel** and **Flight Radio Panel** to **Kerbal Space Program** on macOS via [kRPC](https://krpc.github.io/krpc/).

Uses [hidapi](https://github.com/libusb/hidapi) (IOKit backend) to access the panels — not gousb/libusb, which cannot claim HID devices owned by the macOS kernel driver.

Both panels are **optional**: the program works with either one or both connected.

## Requirements

- macOS (tested on Apple Silicon)
- [Go](https://go.dev/) 1.21+
- Logitech/Saitek Flight Switch Panel (USB VID `06a3`, PID `0d67`) and/or Flight Radio Panel (PID `0d05`)
- Kerbal Space Program with the [kRPC mod](https://krpc.github.io/krpc/) installed and running
- (Optional) [KRPC.MechJeb](https://github.com/Genhis/KRPC.MechJeb) for airplane autopilot control

## Installation

```bash
git clone https://github.com/eontech64/ksp-switchpanel
cd ksp-switchpanel
go build -o ksp-switchpanel .          # CLI binary
go build -o ksp-switchpanel-ui ./cmd/ui  # GUI binary (status window)
```

## Usage

Two binaries are provided — choose whichever fits your workflow:

| Binary | Description |
|--------|-------------|
| `ksp-switchpanel` | Headless CLI — runs in a terminal, exit with Ctrl+C |
| `ksp-switchpanel-ui` | Native macOS status window with a Stop button |

### Workflow

1. Connect one or both panels via USB
2. Launch KSP and load a vessel (launchpad or in flight)
3. Start the kRPC server in-game (toolbar → kRPC → Start server, default port 50000)
4. Run the bridge (can be started before KSP — it retries every second):

```bash
./ksp-switchpanel-ui   # or ./ksp-switchpanel
```

The bridge detects which panels are connected, retries kRPC automatically until KSP is ready, and reads the initial switch/rotary positions on startup. When a mission ends and a new one begins, the bridge reconnects automatically and restores all switch states on the new vessel.

### Status window (GUI binary)

```
KSP Panel Bridge  v1.0.0
────────────────────────
Switch Panel   ● Connected
Radio Panel    ● Connected

kRPC           ● Connected
Active Vessel  Jebediah's Rocket
MechJeb        ● Ready

Speed Hold     ● ON  89 m/s
Alt Hold       ○ OFF  240 m

        [ Stop ]
```

## Configuration

Switch actions and radio display modes can be customised by editing **`panels.toml`**, placed next to the binary (or in the directory where you run it). If the file is absent, built-in defaults are used — identical to the tables below.

A fully-commented `panels.toml` with all options is included in the repository.

### Available switch actions

| Value | Effect |
|-------|--------|
| `rcs` | Toggle RCS thrusters |
| `sas` | Toggle SAS autopilot on/off |
| `brakes` | Toggle brakes |
| `gear_down` | Deploy landing gear (on activation only) |
| `gear_up` | Retract landing gear (on activation only) |
| `next_stage` | Activate next stage (on activation only) |
| `action_group:N` | Toggle action group N (1–10) |
| `sas_mode:X` | Set SAS autopilot mode (on activation only) |
| `none` | No action |

**SAS mode values for `sas_mode:X`:**

| X | KSP SAS mode |
|---|--------------|
| `stability_assist` | Stability Assist |
| `prograde` | Prograde |
| `retrograde` | Retrograde |
| `target` | Target (requires target selected) |
| `anti_target` | Anti-Target (requires target selected) |

### Available telemetry fields (radio displays)

| Field | Description |
|-------|-------------|
| `altitude_km` | Mean altitude (km) |
| `vspeed` | Vertical speed (m/s) |
| `apoapsis_km` | Apoapsis altitude (km) |
| `periapsis_km` | Periapsis altitude (km) |
| `speed_kts` | Navball speed (knots) |
| `speed_ms` | Navball speed (m/s) |
| `orbital_speed` | Orbital speed (m/s) |
| `heading` | Compass heading (°) |
| `pitch` | Pitch angle (°) |
| `roll` | Roll angle (°) |
| `latitude` | Latitude (°) |
| `longitude` | Longitude (°) |
| `gforce` | G-force |
| `dynpressure` | Dynamic pressure (Pa) |
| `time_to_apo` | Time to apoapsis (s) |
| `time_to_peri` | Time to periapsis (s) |

## Flight Switch Panel

### Default switch mapping

| Panel switch | KSP action |
|---|---|
| BAT | RCS on/off |
| ALT | SAS on/off |
| AVIONICS | Action Group 1 |
| FUEL | Action Group 2 |
| DE-ICE | Action Group 3 |
| PITOT | Action Group 4 |
| COWL | Action Group 5 |
| PANEL | Action Group 6 |
| BEACON | Action Group 7 |
| NAV | Action Group 8 |
| STROBE | Action Group 9 |
| TAXI | Action Group 10 |
| LANDING | Brakes |
| GEAR DOWN | Deploy landing gear |
| GEAR UP | Retract landing gear |
| ROT OFF | SAS → Stability Assist |
| ROT R | SAS → Prograde |
| ROT L | SAS → Retrograde |
| ROT BOTH | SAS → Target |
| ROT START | SAS → Anti-Target |

The rotary sets the SAS autopilot mode whenever the switch enters that position. The mode takes effect immediately if SAS is active, or is remembered for when SAS is next enabled.

### Landing gear LEDs

The N/L/R LEDs sync automatically with the vessel's gear state every 500ms:
- **Green** = gear deployed
- **Red** = gear retracted
- **Off** = no active vessel / bridge reconnecting

## Flight Radio Panel

The four 5-digit displays show live telemetry from KSP. Each rotary switch selects what its pair of displays shows — **Rotary 1** controls the top displays, **Rotary 2** the bottom displays.

### Default display modes

| Rotary position | Left display | Right display |
|---|---|---|
| COM1 | Altitude (km, 1 decimal) | Vertical speed (m/s) |
| COM2 | Apoapsis (km) | Periapsis (km) |
| NAV1 | Speed (knots, navball) | Heading (°) |
| NAV2 | Pitch (°) | Roll (°) |
| ADF | Latitude | Longitude |
| DME | G-Force | Dynamic pressure (Pa) |
| XPDR | Time to apoapsis (s) | Time to periapsis (s) |

**Notes:**
- NAV1 speed matches the KSP navball — switches automatically between surface and orbital speed depending on the navball mode
- Altitude is shown in km (e.g. `100.0` for 100 km) to avoid 5-digit overflow at high altitudes
- Displays update every 500ms
- Displays blank when the bridge is reconnecting between missions

## MechJeb Airplane Autopilot

If [KRPC.MechJeb](https://github.com/Genhis/KRPC.MechJeb) is installed, the radio panel encoders control the MechJeb airplane autopilot:

| Radio panel control | Action |
|---|---|
| SwAct 1 | Toggle Speed Hold |
| SwAct 2 | Toggle Altitude Hold |
| Encoder 1 inner CW/CCW | Speed target ±1 m/s |
| Encoder 1 outer CW/CCW | Speed target ±10 m/s |
| Encoder 2 inner CW/CCW | Altitude target ±10 m |
| Encoder 2 outer CW/CCW | Altitude target ±100 m |

The current Speed Hold and Altitude Hold state (enabled/target) is displayed in the GUI status window.

> **Note:** The MechJeb GUI may show 0 for targets while the kRPC values are set correctly — this is a known display-only limitation of KRPC.MechJeb. The autopilot controls to the correct target value.

## How it works

```
Switch Panel (USB/HID)  ─┐
                          ├─ go-hid (IOKit) ─ kRPC client ─ KSP
Radio Panel  (USB/HID)  ─┘
```

Both panels are accessed via [`go-hid`](https://github.com/sstallion/go-hid), which uses macOS's native IOKit HID APIs. KSP is controlled through the Go kRPC client [`krpc-go`](https://github.com/atburke/krpc-go).

## Why not gousb/libusb?

On macOS, `libusb` cannot claim HID device interfaces because they are already owned by the IOHIDFamily kernel extension (`LIBUSB_ERROR_ACCESS`). `hidapi` uses IOKit instead, bypassing this restriction.

## Project structure

```
.
├── main.go                  # CLI binary: main loop
├── version.go               # Version constant and changelog
├── panels.toml              # Configuration file (optional, editable)
├── cmd/ui/
│   └── main.go              # GUI binary: status window (fyne)
├── config/
│   └── config.go            # Config loader (TOML) with built-in defaults
├── internal/bridge/
│   ├── bridge.go            # Shared bridge logic (RunBridge, handleSwitch, …)
│   └── status.go            # BridgeStatus struct
├── switchpanel/
│   └── switchpanel.go       # Switch panel HID access via go-hid
├── radiopanel/
│   └── radiopanel.go        # Radio panel HID access + display protocol
├── mechjeb/
│   └── mechjeb.gen.go       # Generated kRPC bindings for KRPC.MechJeb
└── hidtest/
    └── main.go              # Standalone tool to verify panel connectivity
```

## License

MIT
