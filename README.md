# ksp-switchpanel

Connects **Logitech/Saitek Flight Switch Panel** and **Flight Radio Panel** to **Kerbal Space Program** on macOS via [kRPC](https://krpc.github.io/krpc/).

Uses [hidapi](https://github.com/libusb/hidapi) (IOKit backend) to access the panels — not gousb/libusb, which cannot claim HID devices owned by the macOS kernel driver.

Both panels are **optional**: the program works with either one or both connected.

## Requirements

- macOS (tested on Apple Silicon)
- [Go](https://go.dev/) 1.21+
- Logitech/Saitek Flight Switch Panel (USB VID `06a3`, PID `0d67`) and/or Flight Radio Panel (PID `0d05`)
- Kerbal Space Program with the [kRPC mod](https://krpc.github.io/krpc/) installed and running

## Installation

```bash
git clone https://github.com/eontech64/ksp-switchpanel
cd ksp-switchpanel
go build -o ksp-switchpanel .
```

## Usage

1. Launch KSP and load a vessel (launchpad or in flight)
2. Start the kRPC server in-game (toolbar → kRPC → Start server, default port 50000)
3. Connect one or both panels via USB
4. Run the bridge:

```bash
./ksp-switchpanel
```

The program detects which panels are connected, waits automatically until a vessel is active in KSP, and reads the initial switch/rotary positions on startup. Press **Ctrl+C** to exit cleanly.

## Configuration

Switch actions and radio display modes can be customised by editing **`panels.toml`**, placed next to the binary (or in the directory where you run it). If the file is absent, built-in defaults are used — identical to the table below.

```toml
[switches]
BAT      = "rcs"
ALT      = "sas"
AVIONICS = "action_group:1"
# ...

[radio]
COM1 = ["altitude_km:1", "vspeed:0"]
NAV1 = ["speed_kts:0",   "heading:0"]
# ...
```

A fully-commented `panels.toml` with all options is included in the repository.

### Available switch actions

| Value | Effect |
|---|---|
| `rcs` | Toggle RCS thrusters |
| `sas` | Toggle SAS autopilot |
| `brakes` | Toggle brakes |
| `gear_down` | Deploy landing gear (on activation only) |
| `gear_up` | Retract landing gear (on activation only) |
| `next_stage` | Activate next stage (on activation only) |
| `action_group:N` | Toggle action group N (1–10) |
| `none` | No action |

### Available telemetry fields (radio displays)

| Field | Description |
|---|---|
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
| ROT START | Activate next stage |

### Landing gear LEDs

The N/L/R LEDs sync automatically with the vessel's gear state every 500ms:
- **Green** = gear deployed
- **Red** = gear retracted

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
- Altitude is shown in km (e.g. `100.0` for 100km) to avoid 5-digit overflow at high altitudes
- Displays update every 500ms

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
├── main.go                  # Main loop: reads panels, calls kRPC
├── version.go               # Version constant and changelog
├── panels.toml              # Configuration file (optional, editable)
├── config/
│   └── config.go            # Config loader (TOML) with built-in defaults
├── switchpanel/
│   └── switchpanel.go       # Switch panel HID access via go-hid
├── radiopanel/
│   └── radiopanel.go        # Radio panel HID access + display protocol
└── hidtest/
    └── main.go              # Standalone tool to verify panel connectivity
```

## License

MIT
