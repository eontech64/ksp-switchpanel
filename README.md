# ksp-switchpanel

Connects a **Logitech/Saitek Flight Switch Panel** to **Kerbal Space Program** on macOS via [kRPC](https://krpc.github.io/krpc/).

Uses [hidapi](https://github.com/libusb/hidapi) (IOKit backend on macOS) to read the panel — not gousb/libusb, which cannot claim HID devices owned by the macOS kernel driver.

## Requirements

- macOS (tested on Apple Silicon)
- [Go](https://go.dev/) 1.21+
- Logitech/Saitek Flight Switch Panel (USB VID `06a3`, PID `0d67`)
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
3. Connect the switch panel via USB
4. Run the bridge:

```bash
./ksp-switchpanel
```

The program waits automatically until a vessel is active in KSP. Press **Ctrl+C** to exit cleanly.

## Switch mapping

| Panel switch | KSP action |
|---|---|
| BAT | RCS |
| ALT | SAS |
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

The **landing gear LEDs** sync automatically with the vessel's gear state every 500ms:
- Green = gear deployed
- Red = gear retracted

## How it works

```
Switch Panel (USB/HID) → go-hid (IOKit) → kRPC client → KSP
```

The `switchpanel` package handles all USB communication via [`go-hid`](https://github.com/sstallion/go-hid), which uses macOS's native IOKit HID APIs and does not require `libusb`. The main program connects to KSP through the kRPC Go client [`krpc-go`](https://github.com/atburke/krpc-go).

## Why not gousb/libusb?

On macOS, `libusb` cannot claim interfaces of HID devices because they are already owned by the IOHIDFamily kernel extension. Attempting to do so results in `LIBUSB_ERROR_ACCESS`. The `hidapi` library uses IOKit instead, which works correctly with HID devices on macOS.

## Project structure

```
.
├── main.go               # Main loop: reads switches, calls kRPC
├── switchpanel/
│   └── switchpanel.go    # HID panel access via go-hid
└── hidtest/
    └── main.go           # Standalone tool to verify panel connectivity
```

## License

MIT
