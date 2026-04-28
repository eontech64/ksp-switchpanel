// Package radiopanel provides access to the Logitech/Saitek Flight Radio Panel
// using hidapi (IOKit on macOS).
package radiopanel

import (
	"fmt"
	"sync"
	"time"

	hid "github.com/sstallion/go-hid"
)

const (
	VendorID  = 0x06a3
	ProductID = 0x0d05
)

// Display IDs — matches the original fpanels convention.
type DisplayID int

const (
	Display1Active  DisplayID = iota // top-left
	Display1Standby                  // top-right
	Display2Active                   // bottom-left
	Display2Standby                  // bottom-right
)

// Digit encoding constants.
const (
	digitBlank byte = 0x0f
	digitDot   byte = 0xd0
	digitDash  byte = 0xef
)

// SwitchID identifies a switch or encoder event.
type SwitchID int

const (
	Rot1COM1 SwitchID = iota
	Rot1COM2
	Rot1NAV1
	Rot1NAV2
	Rot1ADF
	Rot1DME
	Rot1XPDR
	Rot2COM1
	Rot2COM2
	Rot2NAV1
	Rot2NAV2
	Rot2ADF
	Rot2DME
	Rot2XPDR
	SwAct1
	SwAct2
	Enc1InnerCW
	Enc1InnerCCW
	Enc1OuterCW
	Enc1OuterCCW
	Enc2InnerCW
	Enc2InnerCCW
	Enc2OuterCW
	Enc2OuterCCW
)

// SwitchEvent is emitted when a switch or encoder changes state.
type SwitchEvent struct {
	Switch SwitchID
	On     bool
}

// Panel represents the Logitech Flight Radio Panel.
type Panel struct {
	dev          *hid.Device
	switchCh     chan SwitchEvent
	quit         chan struct{}
	done         chan struct{} // closed when readLoop exits (device disconnected or Close called)
	closeOnce    sync.Once
	mu           sync.Mutex
	displayState [22]byte
	initialState [3]byte
}

// Open opens the radio panel. Returns an error if the device is not found.
func Open() (*Panel, error) {
	if err := hid.Init(); err != nil {
		return nil, fmt.Errorf("hid init: %w", err)
	}
	dev, err := hid.OpenFirst(VendorID, ProductID)
	if err != nil {
		hid.Exit()
		return nil, fmt.Errorf("open radio panel (VID=%04x PID=%04x): %w", VendorID, ProductID, err)
	}
	p := &Panel{
		dev:      dev,
		switchCh: make(chan SwitchEvent, 32),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	// Initialize all displays to blank
	for i := range p.displayState {
		p.displayState[i] = digitBlank
	}
	p.sendDisplay()

	// Read initial switch state so we know the rotary positions on startup.
	// HID devices only report changes, so without this the initial position
	// is unknown until the user moves a switch.
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
	// Blank displays on exit
	p.mu.Lock()
	for i := range p.displayState {
		p.displayState[i] = digitBlank
	}
	p.sendDisplay()
	p.mu.Unlock()
	p.dev.Close()
	hid.Exit()
}

// Done returns a channel that is closed when the panel's read loop exits.
// This happens either when Close is called or when the device is disconnected.
func (p *Panel) Done() <-chan struct{} {
	return p.done
}

// SwitchCh returns the channel that emits switch/encoder events.
func (p *Panel) SwitchCh() <-chan SwitchEvent {
	return p.switchCh
}

// DisplayString writes a string to the given display.
// Supported characters: 0-9, '.', '-', ' '.
// The value is right-aligned and padded with blanks.
func (p *Panel) DisplayString(display DisplayID, s string) {
	if display < Display1Active || display > Display2Standby {
		return
	}
	start := int(display) * 5

	var d [5]byte
	dIdx := 0

	if len(s) > 0 && s[0] == '.' {
		s = " " + s
	}
	for _, c := range s {
		if dIdx > 4 && c != '.' {
			break
		}
		switch {
		case c >= '0' && c <= '9':
			d[dIdx] = byte(c - '0')
			dIdx++
		case c == ' ':
			d[dIdx] = digitBlank
			dIdx++
		case c == '.':
			if dIdx > 0 {
				d[dIdx-1] |= digitDot
			}
		case c == '-':
			d[dIdx] = digitDash
			dIdx++
		default:
			d[dIdx] = digitBlank
			dIdx++
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// right-align
	dIdx--
	for i := 4; i >= 0; i-- {
		if dIdx < 0 {
			p.displayState[start+i] = digitBlank
		} else {
			p.displayState[start+i] = d[dIdx]
		}
		dIdx--
	}
	p.sendDisplay()
}

// DisplayInt writes an integer to the given display.
func (p *Panel) DisplayInt(display DisplayID, n int) {
	p.DisplayString(display, fmt.Sprintf("%d", n))
}

// DisplayFloat writes a float to the given display with the given decimal places.
func (p *Panel) DisplayFloat(display DisplayID, n float64, decimals int) {
	p.DisplayString(display, fmt.Sprintf("%.*f", decimals, n))
}

// DisplayOff blanks all four displays.
func (p *Panel) DisplayOff() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.displayState {
		p.displayState[i] = digitBlank
	}
	p.sendDisplay()
}

// sendDisplay sends the current display state to the device.
// Must be called with p.mu held.
func (p *Panel) sendDisplay() {
	// HID feature report: report ID 0x00 followed by 22 display bytes
	report := make([]byte, 23)
	report[0] = 0x00
	copy(report[1:], p.displayState[:])
	p.dev.SendFeatureReport(report)
}

// switchBits maps bit positions in the 3-byte input report to SwitchIDs.
// Each entry: {byteIndex, bitPosition}.
var switchBits = [...]struct{ byteIdx, bit int }{
	Rot1COM1:     {0, 0},
	Rot1COM2:     {0, 1},
	Rot1NAV1:     {0, 2},
	Rot1NAV2:     {0, 3},
	Rot1ADF:      {0, 4},
	Rot1DME:      {0, 5},
	Rot1XPDR:     {0, 6},
	Rot2COM1:     {0, 7}, // byte0 bit7 per README
	Rot2COM2:     {1, 0},
	Rot2NAV1:     {1, 1},
	Rot2NAV2:     {1, 2},
	Rot2ADF:      {1, 3},
	Rot2DME:      {1, 4},
	Rot2XPDR:     {1, 5},
	SwAct1:       {1, 6},
	SwAct2:       {1, 7},
	Enc1InnerCW:  {2, 0},
	Enc1InnerCCW: {2, 1},
	Enc1OuterCW:  {2, 2},
	Enc1OuterCCW: {2, 3},
	Enc2InnerCW:  {2, 4},
	Enc2InnerCCW: {2, 5},
	Enc2OuterCW:  {2, 6},
	Enc2OuterCCW: {2, 7},
}

func (p *Panel) readLoop() {
	defer close(p.done)
	buf := make([]byte, 4)
	prev := p.initialState

	// Emit "on" events for all bits set in the initial state so the caller
	// knows the current rotary positions without needing to move anything.
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
