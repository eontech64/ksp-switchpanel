// Package switchpanel provides access to the Logitech/Saitek Flight Switch Panel
// using hidapi (IOKit on macOS) instead of libusb/gousb.
package switchpanel

import (
	"fmt"
	"sync"
	"time"

	hid "github.com/sstallion/go-hid"
)

const (
	VendorID  = 0x06a3
	ProductID = 0x0d67
)

// Switch IDs — matches the bit positions in the 3-byte USB report.
type SwitchID int

const (
	SwBat SwitchID = iota
	SwAlternator
	SwAvionics
	SwFuel
	SwDeice
	SwPitot
	SwCowl
	SwPanel
	SwBeacon
	SwNav
	SwStrobe
	SwTaxi
	SwLanding
	RotOff
	RotR
	RotL
	RotBoth
	RotStart
	GearUp
	GearDown
)

// Landing gear LED bitmask values (sent as a single byte feature report).
const (
	LEDNGreen byte = 1 << iota
	LEDLGreen
	LEDRGreen
	LEDNRed
	LEDLRed
	LEDRRed
	LEDNYellow   = LEDNGreen | LEDNRed
	LEDLYellow   = LEDLGreen | LEDLRed
	LEDRYellow   = LEDRGreen | LEDRRed
	LEDAllGreen  = LEDNGreen | LEDLGreen | LEDRGreen
	LEDAllRed    = LEDNRed | LEDLRed | LEDRRed
	LEDAllYellow = LEDNYellow | LEDLYellow | LEDRYellow
)

// SwitchEvent is emitted on the channel returned by SwitchCh() when a switch changes state.
type SwitchEvent struct {
	Switch SwitchID
	On     bool
}

// Panel represents the Saitek Flight Switch Panel.
type Panel struct {
	dev          *hid.Device
	switchCh     chan SwitchEvent
	quit         chan struct{}
	done         chan struct{} // closed when readLoop exits (device disconnected or Close called)
	closeOnce    sync.Once
	mu           sync.Mutex
	ledState     byte
	initialState [3]byte
}

// Open opens the switch panel. Returns an error if the device is not found.
func Open() (*Panel, error) {
	if err := hid.Init(); err != nil {
		return nil, fmt.Errorf("hid init: %w", err)
	}
	dev, err := hid.OpenFirst(VendorID, ProductID)
	if err != nil {
		hid.Exit()
		return nil, fmt.Errorf("open switch panel (VID=%04x PID=%04x): %w", VendorID, ProductID, err)
	}
	p := &Panel{
		dev:      dev,
		switchCh: make(chan SwitchEvent, 32),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	buf := make([]byte, 4)
	if n, err := p.dev.ReadWithTimeout(buf, 300*time.Millisecond); err == nil && n >= 3 {
		copy(p.initialState[:], buf[n-3:n])
	}

	go p.readLoop()
	return p, nil
}

// Close releases the device.
func (p *Panel) Close() {
	p.closeOnce.Do(func() { close(p.quit) })
	p.dev.Close()
	hid.Exit()
}

// Done returns a channel that is closed when the panel's read loop exits.
// This happens either when Close is called or when the device is disconnected.
func (p *Panel) Done() <-chan struct{} {
	return p.done
}

// SwitchCh returns the channel that emits switch events.
func (p *Panel) SwitchCh() <-chan SwitchEvent {
	return p.switchCh
}

// SetLEDs sets the landing gear LEDs. Use the LED* constants.
func (p *Panel) SetLEDs(leds byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ledState = leds
	// HID feature report: report ID 0x00 followed by the LED byte.
	_, err := p.dev.SendFeatureReport([]byte{0x00, leds})
	return err
}

// SetLEDsOn turns on specified LEDs leaving others unchanged.
func (p *Panel) SetLEDsOn(leds byte) error {
	p.mu.Lock()
	state := p.ledState | leds
	p.mu.Unlock()
	return p.SetLEDs(state)
}

// SetLEDsOff turns off specified LEDs leaving others unchanged.
func (p *Panel) SetLEDsOff(leds byte) error {
	p.mu.Lock()
	state := p.ledState &^ leds
	p.mu.Unlock()
	return p.SetLEDs(state)
}

// switchBits maps each SwitchID to its byte index and bit position in the 3-byte report.
var switchBits = [...]struct{ byteIdx, bit int }{
	SwBat:        {0, 0},
	SwAlternator: {0, 1},
	SwAvionics:   {0, 2},
	SwFuel:       {0, 3},
	SwDeice:      {0, 4},
	SwPitot:      {0, 5},
	SwCowl:       {0, 6},
	SwPanel:      {0, 7},
	SwBeacon:     {1, 0},
	SwNav:        {1, 1},
	SwStrobe:     {1, 2},
	SwTaxi:       {1, 3},
	SwLanding:    {1, 4},
	RotOff:       {1, 5},
	RotR:         {1, 6},
	RotL:         {1, 7},
	RotBoth:      {2, 0},
	RotStart:     {2, 1},
	GearUp:       {2, 2},
	GearDown:     {2, 3},
}

func (p *Panel) readLoop() {
	defer close(p.done)
	buf := make([]byte, 4) // hidapi may prepend a report ID byte
	prev := p.initialState

	// Emit "on" events for all switches already active at startup.
	for id := range switchBits {
		bi := switchBits[id].byteIdx
		bit := uint(switchBits[id].bit)
		if (prev[bi]>>bit)&1 == 1 {
			select {
			case p.switchCh <- SwitchEvent{Switch: SwitchID(id), On: true}:
			default:
			}
		}
	}

	for {
		select {
		case <-p.quit:
			return
		default:
		}

		n, err := p.dev.Read(buf)
		if err != nil {
			return
		}
		if n < 3 {
			continue
		}

		// hidapi on macOS does NOT prepend the report ID for devices with a single report descriptor.
		// Take the last 3 bytes to be safe.
		data := buf[n-3 : n]

		for id := range switchBits {
			bi := switchBits[id].byteIdx
			bit := uint(switchBits[id].bit)
			wasOn := (prev[bi]>>bit)&1 == 1
			isOn := (data[bi]>>bit)&1 == 1
			if wasOn != isOn {
				select {
				case p.switchCh <- SwitchEvent{Switch: SwitchID(id), On: isOn}:
				default:
				}
			}
		}
		copy(prev[:], data)
	}
}
