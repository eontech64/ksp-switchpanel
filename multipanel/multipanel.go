// Package multipanel provides access to the Logitech/Saitek Flight Multi Panel
// using hidapi (IOKit on macOS).
package multipanel

import (
	"fmt"
	"sync"
	"time"

	hid "github.com/sstallion/go-hid"
)

const (
	VendorID  = 0x06a3
	ProductID = 0x0d06
)

// DisplayID identifies a row on the segment display.
type DisplayID int

const (
	Row1 DisplayID = iota // top row (5 digits)
	Row2                  // bottom row (5 digits, supports dash)
)

// Digit encoding constants (same BCD encoding as all Logitech panels).
const (
	digitBlank byte = 0x0f
	digitDash  byte = 0xde // only valid on Row2 per hardware spec
)

// Button LED bitmask values (byte 10 of the 12-byte display state).
const (
	LEDAP  byte = 1 << iota // AP button backlight
	LEDHDG                   // HDG button backlight
	LEDNAV                   // NAV button backlight
	LEDIAS                   // IAS button backlight
	LEDALT                   // ALT button backlight
	LEDVS                    // VS button backlight
	LEDAPR                   // APR button backlight
	LEDREV                   // REV button backlight
)

// SwitchID identifies a switch, button, or encoder event.
type SwitchID int

const (
	RotALT      SwitchID = iota // 5-pos rotary: ALT selected
	RotVS                        // 5-pos rotary: VS selected
	RotIAS                       // 5-pos rotary: IAS selected
	RotHDG                       // 5-pos rotary: HDG selected
	RotCRS                       // 5-pos rotary: CRS selected
	EncCW                        // rotary encoder clockwise
	EncCCW                       // rotary encoder counter-clockwise
	BtnAP                        // AP push button
	BtnHDG                       // HDG push button
	BtnNAV                       // NAV push button
	BtnIAS                       // IAS push button
	BtnALT                       // ALT push button
	BtnVS                        // VS push button
	BtnAPR                       // APR push button
	BtnREV                       // REV push button
	AutoThrottle                 // AUTO THROTTLE two-position switch
	FlapsUp                      // FLAPS UP momentary
	FlapsDown                    // FLAPS DOWN momentary
	TrimDown                     // PITCH TRIM down momentary
	TrimUp                       // PITCH TRIM up momentary
)

// SwitchEvent is emitted when a switch, button, or encoder changes state.
type SwitchEvent struct {
	Switch SwitchID
	On     bool
}

// Panel represents the Logitech Flight Multi Panel.
type Panel struct {
	dev          *hid.Device
	switchCh     chan SwitchEvent
	quit         chan struct{}
	done         chan struct{} // closed when readLoop exits (device disconnected or Close called)
	closeOnce    sync.Once
	mu           sync.Mutex
	displayState [12]byte // [0-4] row1, [5-9] row2, [10] LEDs, [11] 0xff
	initialState [3]byte
}

// switchBits maps each SwitchID to its byte index and bit position in the 3-byte HID input report.
var switchBits = [...]struct{ byteIdx, bit int }{
	RotALT:       {0, 0},
	RotVS:        {0, 1},
	RotIAS:       {0, 2},
	RotHDG:       {0, 3},
	RotCRS:       {0, 4},
	EncCW:        {0, 5},
	EncCCW:       {0, 6},
	BtnAP:        {0, 7},
	BtnHDG:       {1, 0},
	BtnNAV:       {1, 1},
	BtnIAS:       {1, 2},
	BtnALT:       {1, 3},
	BtnVS:        {1, 4},
	BtnAPR:       {1, 5},
	BtnREV:       {1, 6},
	AutoThrottle: {1, 7},
	FlapsUp:      {2, 0},
	FlapsDown:    {2, 1},
	TrimDown:     {2, 2},
	TrimUp:       {2, 3},
}

// Open opens the multi panel. Returns an error if the device is not found.
func Open() (*Panel, error) {
	if err := hid.Init(); err != nil {
		return nil, fmt.Errorf("hid init: %w", err)
	}
	dev, err := hid.OpenFirst(VendorID, ProductID)
	if err != nil {
		hid.Exit()
		return nil, fmt.Errorf("open multi panel (VID=%04x PID=%04x): %w", VendorID, ProductID, err)
	}
	p := &Panel{
		dev:      dev,
		switchCh: make(chan SwitchEvent, 32),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	// Initialize display: all digits blank, all LEDs off.
	for i := 0; i < 10; i++ {
		p.displayState[i] = digitBlank
	}
	p.displayState[10] = 0x00
	p.displayState[11] = 0xff
	p.sendDisplay()

	// Read initial switch state so the rotary position is known on startup.
	buf := make([]byte, 4)
	if n, err := p.dev.ReadWithTimeout(buf, 300*time.Millisecond); err == nil && n >= 3 {
		copy(p.initialState[:], buf[n-3:n])
	}

	go p.readLoop()
	return p, nil
}

// Close blanks the display, turns off LEDs, and releases the device.
func (p *Panel) Close() {
	p.closeOnce.Do(func() { close(p.quit) })
	p.mu.Lock()
	for i := 0; i < 10; i++ {
		p.displayState[i] = digitBlank
	}
	p.displayState[10] = 0x00
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

// SwitchCh returns the channel that emits switch/button/encoder events.
func (p *Panel) SwitchCh() <-chan SwitchEvent {
	return p.switchCh
}

// DisplayString writes a string to the given display row.
// Supported characters: 0-9, ' ' (blank), '-' (dash, Row2 only).
// The value is right-aligned and padded with blanks.
func (p *Panel) DisplayString(display DisplayID, s string) {
	if display != Row1 && display != Row2 {
		return
	}
	start := int(display) * 5
	var d [5]byte
	dIdx := 0
	for _, c := range s {
		if dIdx > 4 {
			break
		}
		switch {
		case c >= '0' && c <= '9':
			d[dIdx] = byte(c - '0')
			dIdx++
		case c == ' ':
			d[dIdx] = digitBlank
			dIdx++
		case c == '-' && display == Row2:
			// Dash is only supported on the bottom row per hardware spec.
			d[dIdx] = digitDash
			dIdx++
		default:
			d[dIdx] = digitBlank
			dIdx++
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Right-align: fill from the right, padding blanks on the left.
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

// DisplayInt writes an integer to the given display row.
func (p *Panel) DisplayInt(display DisplayID, n int) {
	p.DisplayString(display, fmt.Sprintf("%d", n))
}

// DisplayFloat writes a float to the given display row with the given number of decimal places.
// Note: the multi panel display has no dot support; decimals are shown as-is in the integer digits.
func (p *Panel) DisplayFloat(display DisplayID, n float64, decimals int) {
	p.DisplayString(display, fmt.Sprintf("%.*f", decimals, n))
}

// DisplayOff blanks both display rows.
func (p *Panel) DisplayOff() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := 0; i < 10; i++ {
		p.displayState[i] = digitBlank
	}
	p.sendDisplay()
}

// SetLEDs sets all button LEDs at once. Use the LED* constants OR'd together.
func (p *Panel) SetLEDs(leds byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.displayState[10] = leds
	p.sendDisplay()
}

// SetLEDsOn turns on the given LEDs leaving all others unchanged.
func (p *Panel) SetLEDsOn(leds byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.displayState[10] |= leds
	p.sendDisplay()
}

// SetLEDsOff turns off the given LEDs leaving all others unchanged.
func (p *Panel) SetLEDsOff(leds byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.displayState[10] &^= leds
	p.sendDisplay()
}

// sendDisplay sends the current 12-byte display/LED state to the device via HID feature report.
// Must be called with p.mu held.
func (p *Panel) sendDisplay() {
	// HID feature report: prepend report ID 0x00, then the 12 display bytes.
	report := make([]byte, 13)
	report[0] = 0x00
	copy(report[1:], p.displayState[:])
	p.dev.SendFeatureReport(report)
}

func (p *Panel) readLoop() {
	defer close(p.done)
	buf := make([]byte, 4)
	prev := p.initialState

	// Emit On=true for any switch already active (e.g. the current rotary position).
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

		// hidapi on macOS does NOT prepend the report ID; take the last 3 bytes.
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
