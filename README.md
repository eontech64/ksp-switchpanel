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

## Flight Switch Panel

### Switch mapping

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
├── switchpanel/
│   └── switchpanel.go       # Switch panel HID access via go-hid
├── radiopanel/
│   └── radiopanel.go        # Radio panel HID access + display protocol
└── hidtest/
    └── main.go              # Standalone tool to verify panel connectivity
```

## License

MIT
