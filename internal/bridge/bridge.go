// Package bridge contains the kRPC ↔ panel bridge logic, shared between
// the CLI and GUI binaries.
package bridge

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	krpcgo "github.com/atburke/krpc-go"
	"github.com/atburke/krpc-go/spacecenter"

	"ksp-switchpanel/config"
	"ksp-switchpanel/mechjeb"
	"ksp-switchpanel/multipanel"
	"ksp-switchpanel/radiopanel"
	"ksp-switchpanel/switchpanel"
)

// switchConfigName maps each SwitchID to its key in the [switches] config section.
var switchConfigName = map[switchpanel.SwitchID]string{
	switchpanel.SwBat:        "BAT",
	switchpanel.SwAlternator: "ALT",
	switchpanel.SwAvionics:   "AVIONICS",
	switchpanel.SwFuel:       "FUEL",
	switchpanel.SwDeice:      "DEICE",
	switchpanel.SwPitot:      "PITOT",
	switchpanel.SwCowl:       "COWL",
	switchpanel.SwPanel:      "PANEL",
	switchpanel.SwBeacon:     "BEACON",
	switchpanel.SwNav:        "NAV",
	switchpanel.SwStrobe:     "STROBE",
	switchpanel.SwTaxi:       "TAXI",
	switchpanel.SwLanding:    "LANDING",
	switchpanel.GearUp:       "GEAR_UP",
	switchpanel.GearDown:     "GEAR_DOWN",
	switchpanel.RotOff:       "ROT_OFF",
	switchpanel.RotR:         "ROT_R",
	switchpanel.RotL:         "ROT_L",
	switchpanel.RotBoth:      "ROT_BOTH",
	switchpanel.RotStart:     "ROT_START",
}

// rotaryModeName maps each radio rotary SwitchID to its mode name used in [radio] config.
var rotaryModeName = map[radiopanel.SwitchID]string{
	radiopanel.Rot1COM1: "COM1", radiopanel.Rot2COM1: "COM1",
	radiopanel.Rot1COM2: "COM2", radiopanel.Rot2COM2: "COM2",
	radiopanel.Rot1NAV1: "NAV1", radiopanel.Rot2NAV1: "NAV1",
	radiopanel.Rot1NAV2: "NAV2", radiopanel.Rot2NAV2: "NAV2",
	radiopanel.Rot1ADF:  "ADF",  radiopanel.Rot2ADF:  "ADF",
	radiopanel.Rot1DME:  "DME",  radiopanel.Rot2DME:  "DME",
	radiopanel.Rot1XPDR: "XPDR", radiopanel.Rot2XPDR: "XPDR",
}

var rot1Switch = map[radiopanel.SwitchID]bool{
	radiopanel.Rot1COM1: true, radiopanel.Rot1COM2: true,
	radiopanel.Rot1NAV1: true, radiopanel.Rot1NAV2: true,
	radiopanel.Rot1ADF:  true, radiopanel.Rot1DME:  true,
	radiopanel.Rot1XPDR: true,
}

type telemetryFn func(f *spacecenter.Flight, o *spacecenter.Orbit, navSpeed float64) (float64, error)

var telemetryFns = map[string]telemetryFn{
	"altitude_km": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := f.MeanAltitude()
		return v / 1000, err
	},
	"vspeed": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		return f.VerticalSpeed()
	},
	"apoapsis_km": func(_ *spacecenter.Flight, o *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := o.ApoapsisAltitude()
		return v / 1000, err
	},
	"periapsis_km": func(_ *spacecenter.Flight, o *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := o.PeriapsisAltitude()
		return v / 1000, err
	},
	"speed_kts": func(_ *spacecenter.Flight, _ *spacecenter.Orbit, ns float64) (float64, error) {
		return ns * 1.94384, nil
	},
	"speed_ms": func(_ *spacecenter.Flight, _ *spacecenter.Orbit, ns float64) (float64, error) {
		return ns, nil
	},
	"orbital_speed": func(_ *spacecenter.Flight, o *spacecenter.Orbit, _ float64) (float64, error) {
		return o.Speed()
	},
	"heading": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := f.Heading()
		return float64(v), err
	},
	"pitch": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := f.Pitch()
		return float64(v), err
	},
	"roll": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := f.Roll()
		return float64(v), err
	},
	"latitude": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		return f.Latitude()
	},
	"longitude": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		return f.Longitude()
	},
	"gforce": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := f.GForce()
		return float64(v), err
	},
	"dynpressure": func(f *spacecenter.Flight, _ *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := f.DynamicPressure()
		return float64(v), err
	},
	"time_to_apo": func(_ *spacecenter.Flight, o *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := o.TimeToApoapsis()
		return math.Abs(v), err
	},
	"time_to_peri": func(_ *spacecenter.Flight, o *spacecenter.Orbit, _ float64) (float64, error) {
		v, err := o.TimeToPeriapsis()
		return math.Abs(v), err
	},
}

// RunBridge runs the full kRPC ↔ panel bridge until quitCh is closed.
// It manages panel connections internally, retrying on disconnect or hot-plug.
// statusCh is closed when RunBridge returns.
func RunBridge(
	statusCh chan<- BridgeStatus,
	quitCh <-chan struct{},
	cfg *config.Config,
) {
	defer close(statusCh)

	send := func(s BridgeStatus) {
		select {
		case statusCh <- s:
		default:
		}
	}

	var swPanel *switchpanel.Panel
	var radioPanel *radiopanel.Panel
	var multiPanel *multipanel.Panel

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	rot1Mode := "COM1"
	rot2Mode := "COM1"
	multiRotMode := "ALT"

	// switchPos tracks the physical position of each switch across sessions.
	// When reconnecting, these are re-applied to the new vessel.
	switchPos := make(map[switchpanel.SwitchID]bool)

	kRPCCfg := krpcgo.KRPCClientConfig{
		ClientName: "KSP-Panels",
		RPCOnly:    true,
	}

	for {
		// Release and nil any panels that have been disconnected.
		if swPanel != nil {
			select {
			case <-swPanel.Done():
				log.Println("Switch panel disconnected.")
				swPanel.Close()
				swPanel = nil
			default:
			}
		}
		if radioPanel != nil {
			select {
			case <-radioPanel.Done():
				log.Println("Radio panel disconnected.")
				radioPanel.Close()
				radioPanel = nil
			default:
			}
		}
		if multiPanel != nil {
			select {
			case <-multiPanel.Done():
				log.Println("Multi panel disconnected.")
				multiPanel.Close()
				multiPanel = nil
			default:
			}
		}

		// Try to open any panels not currently connected.
		if swPanel == nil {
			if p, err := switchpanel.Open(); err == nil {
				log.Println("Switch panel connected.")
				swPanel = p
			}
		}
		if radioPanel == nil {
			if p, err := radiopanel.Open(); err == nil {
				log.Println("Radio panel connected.")
				radioPanel = p
			}
		}
		if multiPanel == nil {
			if p, err := multipanel.Open(); err == nil {
				log.Println("Multi panel connected.")
				multiPanel = p
			}
		}

		hasSW := swPanel != nil
		hasRD := radioPanel != nil
		hasMU := multiPanel != nil

		if !hasSW && !hasRD && !hasMU {
			log.Println("No panels found. Waiting for a panel to be connected...")
			send(BridgeStatus{KRPC: "connecting"})
			select {
			case <-quitCh:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// Derive channels fresh each time so reconnected panels get their new channels.
		var swCh <-chan switchpanel.SwitchEvent
		if hasSW {
			swCh = swPanel.SwitchCh()
		}
		var radioCh <-chan radiopanel.SwitchEvent
		if hasRD {
			radioCh = radioPanel.SwitchCh()
		}
		var multiCh <-chan multipanel.SwitchEvent
		if hasMU {
			multiCh = multiPanel.SwitchCh()
		}

		send(BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, MultiPanel: hasMU, KRPC: "connecting"})

		ctx, cancelCtx := context.WithCancel(context.Background())

		log.Println("Connecting to kRPC server...")
		client := krpcgo.NewKRPCClient(kRPCCfg)
		if err := client.Connect(ctx); err != nil {
			log.Printf("kRPC connect failed: %v — retrying in 1s...", err)
			cancelCtx()
			showClock(radioPanel)
			select {
			case <-quitCh:
				closePanels(swPanel, radioPanel, multiPanel)
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}
		log.Println("kRPC connected.")
		send(BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, MultiPanel: hasMU, KRPC: "connected"})

		sc := spacecenter.New(client)
		vessel, control, ok := waitForVessel(sc, swPanel, radioPanel, quitCh)
		if !ok {
			client.Close()
			cancelCtx()
			closePanels(swPanel, radioPanel, multiPanel)
			return
		}

		// Re-apply remembered switch positions to the new vessel.
		reapplySwitches(switchPos, cfg.Switches, control)

		mj := mechjeb.New(client)
		var ap *mechjeb.AirplaneAutopilot
		var smartass *mechjeb.SmartASS
		// MechJeb initializes asynchronously in LateUpdate; retry for up to 5 seconds.
		for retry := 0; retry <= 5; retry++ {
			ready, err := mj.APIReady()
			if err != nil {
				log.Printf("MechJeb service not available (is KRPC.MechJeb installed?): %v", err)
				break
			}
			if ready {
				log.Println("MechJeb APIReady=true, getting AirplaneAutopilot and SmartASS...")
				if a, err := mj.AirplaneAutopilot(); err != nil {
					log.Printf("MechJeb AirplaneAutopilot unavailable: %v", err)
				} else {
					ap = a
					log.Println("MechJeb AirplaneAutopilot ready.")
				}
				if s, err := mj.SmartASS(); err != nil {
					log.Printf("MechJeb SmartASS unavailable: %v", err)
				} else {
					smartass = s
					log.Println("MechJeb SmartASS ready.")
				}
				break
			}
			if retry < 5 {
				log.Printf("MechJeb APIReady=false, waiting for MechJeb to initialize (%d/5)...", retry+1)
				time.Sleep(1 * time.Second)
			} else {
				log.Println("MechJeb APIReady=false after retries — MechJeb not loaded or no MechJeb part on vessel.")
			}
		}

		name, _ := vessel.Name()
		base := BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, MultiPanel: hasMU, KRPC: "connected", VesselName: name, MechJeb: ap != nil}
		send(base)

		log.Println("Ready.")
		vesselLost := runSession(ctx, vessel, control, ap, smartass, swPanel, radioPanel, multiPanel, cfg,
			swCh, radioCh, multiCh, ticker.C, quitCh, &rot1Mode, &rot2Mode, &multiRotMode,
			statusCh, base, switchPos)

		client.Close()
		cancelCtx()

		if !vesselLost {
			closePanels(swPanel, radioPanel, multiPanel)
			return
		}

		// Session ended (vessel lost or panel disconnected) — blank whatever is still connected.
		if swPanel != nil {
			swPanel.SetLEDs(0)
		}
		if radioPanel != nil {
			radioPanel.DisplayOff()
		}
		if multiPanel != nil {
			multiPanel.SetLEDs(0)
			multiPanel.DisplayOff()
		}
		log.Println("Session ended. Reconnecting...")
		select {
		case <-quitCh:
			closePanels(swPanel, radioPanel, multiPanel)
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func closePanels(swPanel *switchpanel.Panel, radioPanel *radiopanel.Panel, multiPanel *multipanel.Panel) {
	if swPanel != nil {
		swPanel.SetLEDs(0)
		swPanel.Close()
	}
	if radioPanel != nil {
		radioPanel.DisplayOff()
		radioPanel.Close()
	}
	if multiPanel != nil {
		multiPanel.SetLEDs(0)
		multiPanel.DisplayOff()
		multiPanel.Close()
	}
}

// showClock writes the current local time to the radio panel's four displays.
//
//	Display1Active  (top-left):    DD.MM  — day and month
//	Display1Standby (top-right):   YYYY   — year
//	Display2Active  (bottom-left): HH.MM  — hour and minute
//	Display2Standby (bottom-right): SS    — seconds (live pulse)
func showClock(rp *radiopanel.Panel) {
	if rp == nil {
		return
	}
	now := time.Now()
	rp.DisplayString(radiopanel.Display1Active, fmt.Sprintf("%02d.%02d", now.Day(), int(now.Month())))
	rp.DisplayString(radiopanel.Display1Standby, fmt.Sprintf("%d", now.Year()))
	rp.DisplayString(radiopanel.Display2Active, fmt.Sprintf("%02d.%02d", now.Hour(), now.Minute()))
	rp.DisplayString(radiopanel.Display2Standby, fmt.Sprintf("%02d", now.Second()))
}

func waitForVessel(sc *spacecenter.SpaceCenter, swPanel *switchpanel.Panel, radioPanel *radiopanel.Panel, quitCh <-chan struct{}) (*spacecenter.Vessel, *spacecenter.Control, bool) {
	log.Println("Waiting for active vessel...")
	for {
		vessel, err := sc.ActiveVessel()
		if err != nil {
			showClock(radioPanel)
			select {
			case <-quitCh:
				return nil, nil, false
			case <-time.After(1 * time.Second):
			}
			continue
		}
		control, err := vessel.Control()
		if err != nil {
			showClock(radioPanel)
			select {
			case <-quitCh:
				return nil, nil, false
			case <-time.After(1 * time.Second):
			}
			continue
		}
		name, _ := vessel.Name()
		log.Printf("Active vessel: %s", name)
		if swPanel != nil {
			swPanel.SetLEDs(0)
		}
		return vessel, control, true
	}
}

func runSession(
	ctx context.Context,
	vessel *spacecenter.Vessel,
	control *spacecenter.Control,
	ap *mechjeb.AirplaneAutopilot,
	smartass *mechjeb.SmartASS,
	swPanel *switchpanel.Panel,
	radioPanel *radiopanel.Panel,
	multiPanel *multipanel.Panel,
	cfg *config.Config,
	swCh <-chan switchpanel.SwitchEvent,
	radioCh <-chan radiopanel.SwitchEvent,
	multiCh <-chan multipanel.SwitchEvent,
	tickC <-chan time.Time,
	quitCh <-chan struct{},
	rot1Mode, rot2Mode *string,
	multiRotMode *string,
	statusCh chan<- BridgeStatus,
	base BridgeStatus,
	switchPos map[switchpanel.SwitchID]bool,
) bool {
	errCount := 0
	const maxErrors = 3
	gearState := &gearLEDState{}
	atArmed := false // tracks AUTO THROTTLE switch: true=ARM, false=OFF

	sendStatus := func(s BridgeStatus) {
		select {
		case statusCh <- s:
		default:
		}
	}

	lastAP := BridgeStatus{}

	// Done channels — nil channel never fires, so panels that are nil are safe.
	var swDone, radioDone, multiDone <-chan struct{}
	if swPanel != nil {
		swDone = swPanel.Done()
	}
	if radioPanel != nil {
		radioDone = radioPanel.Done()
	}
	if multiPanel != nil {
		multiDone = multiPanel.Done()
	}

	for {
		select {
		case <-ctx.Done():
			return false
		case <-quitCh:
			return false
		case <-swDone:
			log.Println("Switch panel disconnected during session.")
			return true
		case <-radioDone:
			log.Println("Radio panel disconnected during session.")
			return true
		case <-multiDone:
			log.Println("Multi panel disconnected during session.")
			return true
		case ev := <-swCh:
			switchPos[ev.Switch] = ev.On
			handleSwitch(ev, cfg.Switches, control)
		case ev := <-radioCh:
			handleRadioSwitch(ev, rot1Mode, rot2Mode)
			if ap != nil {
				handleAutopilot(ev, ap)
			}
		case ev := <-multiCh:
			// AutoThrottle position is tracked regardless of MechJeb availability.
			if ev.Switch == multipanel.AutoThrottle {
				atArmed = ev.On
				log.Printf("Multi AutoThrottle: armed=%v", ev.On)
			} else if ap != nil {
				handleMultiSwitch(ev, multiRotMode, ap, smartass, atArmed, control)
			}
		case <-tickC:
			tickOK := true
			if swPanel != nil && !syncLEDs(control, swPanel, gearState) {
				tickOK = false
			}
			if radioPanel != nil && !updateDisplays(radioPanel, vessel, control, *rot1Mode, *rot2Mode, cfg.Radio) {
				tickOK = false
			}
			if multiPanel != nil && ap != nil {
				ledOK := syncMultiLEDs(multiPanel, ap, smartass, atArmed)
				var dispOK bool
				if atArmed {
					dispOK = updateMultiDisplay(multiPanel, vessel, ap, *multiRotMode)
				} else {
					multiPanel.DisplayOff()
					dispOK = true
				}
				if !ledOK || !dispOK {
					tickOK = false
				}
			}
			if !tickOK {
				errCount++
				if errCount >= maxErrors {
					return true
				}
			} else {
				errCount = 0
			}
			// Send AP status when it changes.
			if ap != nil {
				cur := readAPStatus(ap, base)
				if cur != lastAP {
					sendStatus(cur)
					lastAP = cur
				}
			}
		}
	}
}

func handleSwitch(ev switchpanel.SwitchEvent, switchCfg map[string]string, ctrl *spacecenter.Control) {
	name, ok := switchConfigName[ev.Switch]
	if !ok {
		return
	}
	action, ok := switchCfg[name]
	if !ok || action == "none" || action == "" {
		return
	}
	on := ev.On
	switch {
	case action == "rcs":
		ctrl.SetRCS(on)
	case action == "sas":
		ctrl.SetSAS(on)
	case action == "brakes":
		ctrl.SetBrakes(on)
	case action == "gear_down" && on:
		ctrl.SetGear(true)
	case action == "gear_up" && on:
		ctrl.SetGear(false)
	case action == "next_stage" && on:
		ctrl.ActivateNextStage()
	case strings.HasPrefix(action, "action_group:"):
		n, err := strconv.Atoi(strings.TrimPrefix(action, "action_group:"))
		if err != nil {
			return
		}
		// kRPC action groups are 0-indexed (0=AG1 … 9=AG10); config uses 1-10.
		if n < 1 || n > 10 {
			return
		}
		ctrl.SetActionGroup(uint32(n-1), on)
	case strings.HasPrefix(action, "sas_mode:") && on:
		sasModeLookup := map[string]spacecenter.SASMode{
			"stability_assist": spacecenter.SASMode_StabilityAssist,
			"prograde":         spacecenter.SASMode_Prograde,
			"retrograde":       spacecenter.SASMode_Retrograde,
			"target":           spacecenter.SASMode_Target,
			"anti_target":      spacecenter.SASMode_AntiTarget,
		}
		if m, ok := sasModeLookup[strings.TrimPrefix(action, "sas_mode:")]; ok {
			ctrl.SetSASMode(m)
		}
	}
}

func handleRadioSwitch(ev radiopanel.SwitchEvent, rot1Mode, rot2Mode *string) {
	if !ev.On {
		return
	}
	modeName, ok := rotaryModeName[ev.Switch]
	if !ok {
		return
	}
	if rot1Switch[ev.Switch] {
		*rot1Mode = modeName
		log.Printf("Radio top -> %s", modeName)
	} else {
		*rot2Mode = modeName
		log.Printf("Radio bottom -> %s", modeName)
	}
}

func handleAutopilot(ev radiopanel.SwitchEvent, ap *mechjeb.AirplaneAutopilot) {
	if !ev.On {
		return
	}
	switch ev.Switch {
	case radiopanel.SwAct1:
		enabled, err := ap.SpeedHoldEnabled()
		if err != nil {
			return
		}
		ap.SetSpeedHoldEnabled(!enabled)
		log.Printf("Autopilot SpeedHold: %v", !enabled)
	case radiopanel.SwAct2:
		enabled, err := ap.AltitudeHoldEnabled()
		if err != nil {
			return
		}
		ap.SetAltitudeHoldEnabled(!enabled)
		log.Printf("Autopilot AltitudeHold: %v", !enabled)
	case radiopanel.Enc1InnerCW:
		adjustSpeed(ap, +1)
	case radiopanel.Enc1InnerCCW:
		adjustSpeed(ap, -1)
	case radiopanel.Enc1OuterCW:
		adjustSpeed(ap, +10)
	case radiopanel.Enc1OuterCCW:
		adjustSpeed(ap, -10)
	case radiopanel.Enc2InnerCW:
		adjustAltitude(ap, +10)
	case radiopanel.Enc2InnerCCW:
		adjustAltitude(ap, -10)
	case radiopanel.Enc2OuterCW:
		adjustAltitude(ap, +100)
	case radiopanel.Enc2OuterCCW:
		adjustAltitude(ap, -100)
	}
}

func adjustSpeed(ap *mechjeb.AirplaneAutopilot, delta float64) {
	current, err := ap.SpeedTarget()
	if err != nil {
		return
	}
	next := current + delta
	if next < 0 {
		next = 0
	}
	ap.SetSpeedTarget(next)
	log.Printf("Autopilot SpeedTarget: %.0f kt", next)
}

func adjustAltitude(ap *mechjeb.AirplaneAutopilot, delta float64) {
	current, err := ap.AltitudeTarget()
	if err != nil {
		return
	}
	next := current + delta
	if next < 0 {
		next = 0
	}
	ap.SetAltitudeTarget(next)
	log.Printf("Autopilot AltitudeTarget: %.0f m", next)
}

func updateDisplays(rp *radiopanel.Panel, vessel *spacecenter.Vessel, ctrl *spacecenter.Control,
	rot1Mode, rot2Mode string, radioCfg map[string][]string) bool {

	orbit, err := vessel.Orbit()
	if err != nil {
		return false
	}
	body, err := orbit.Body()
	if err != nil {
		return false
	}
	bodyRef, err := body.ReferenceFrame()
	if err != nil {
		return false
	}
	flight, err := vessel.Flight(bodyRef)
	if err != nil {
		return false
	}

	var navSpeed float64
	if speedMode, err := ctrl.SpeedMode(); err == nil {
		if speedMode == spacecenter.SpeedMode_Orbit {
			navSpeed, _ = orbit.Speed()
		} else {
			navSpeed, _ = flight.Speed()
		}
	}

	writeDisplayPair(rp, flight, orbit, navSpeed, radioCfg, rot1Mode,
		radiopanel.Display1Active, radiopanel.Display1Standby)
	writeDisplayPair(rp, flight, orbit, navSpeed, radioCfg, rot2Mode,
		radiopanel.Display2Active, radiopanel.Display2Standby)
	return true
}

func writeDisplayPair(rp *radiopanel.Panel, flight *spacecenter.Flight, orbit *spacecenter.Orbit,
	navSpeed float64, radioCfg map[string][]string, mode string,
	left, right radiopanel.DisplayID) {

	specs, ok := radioCfg[mode]
	if !ok || len(specs) < 2 {
		return
	}
	leftSpec, err := config.ParseDisplaySpec(specs[0])
	if err != nil {
		return
	}
	rightSpec, err := config.ParseDisplaySpec(specs[1])
	if err != nil {
		return
	}
	writeDisplay(rp, left, leftSpec, flight, orbit, navSpeed)
	writeDisplay(rp, right, rightSpec, flight, orbit, navSpeed)
}

func writeDisplay(rp *radiopanel.Panel, display radiopanel.DisplayID, spec config.DisplaySpec,
	flight *spacecenter.Flight, orbit *spacecenter.Orbit, navSpeed float64) {

	fn, ok := telemetryFns[spec.Field]
	if !ok {
		return
	}
	v, err := fn(flight, orbit, navSpeed)
	if err != nil {
		return
	}
	if spec.Decimals == 0 {
		rp.DisplayInt(display, int(v))
	} else {
		rp.DisplayFloat(display, v, spec.Decimals)
	}
}

// gearLEDState tracks gear position across ticks to detect transitions.
type gearLEDState struct {
	initialized     bool
	lastDeployed    bool
	transitionUntil time.Time
}

// syncLEDs updates the landing gear LEDs:
//   - Gear DOWN → all green
//   - Gear UP   → all off
//   - Transition (5 s after state change) → all red
//
// Returns false if the vessel reference appears to be invalid.
func syncLEDs(ctrl *spacecenter.Control, panel *switchpanel.Panel, state *gearLEDState) bool {
	deployed, err := ctrl.Gear()
	if err != nil {
		return false
	}

	if !state.initialized {
		state.lastDeployed = deployed
		state.initialized = true
	} else if deployed != state.lastDeployed {
		state.transitionUntil = time.Now().Add(5 * time.Second)
		state.lastDeployed = deployed
	}

	switch {
	case time.Now().Before(state.transitionUntil):
		panel.SetLEDs(switchpanel.LEDAllRed)
	case deployed:
		panel.SetLEDs(switchpanel.LEDAllGreen)
	default:
		panel.SetLEDs(0)
	}
	return true
}

// reapplySwitches sends the remembered switch positions to a freshly connected vessel.
func reapplySwitches(pos map[switchpanel.SwitchID]bool, switchCfg map[string]string, ctrl *spacecenter.Control) {
	if len(pos) == 0 {
		return
	}
	log.Println("Re-applying switch positions to new vessel...")
	for id, on := range pos {
		handleSwitch(switchpanel.SwitchEvent{Switch: id, On: on}, switchCfg, ctrl)
	}
}

// readAPStatus reads the current MechJeb airplane autopilot state and returns
// a BridgeStatus with the AP fields filled in (other fields copied from base).
func readAPStatus(ap *mechjeb.AirplaneAutopilot, base BridgeStatus) BridgeStatus {
	s := base
	s.APSpeedHold, _ = ap.SpeedHoldEnabled()
	s.APAltHold, _ = ap.AltitudeHoldEnabled()
	s.APSpeedTarget, _ = ap.SpeedTarget()
	s.APAltTarget, _ = ap.AltitudeTarget()
	return s
}

func radioModeNameStr(id radiopanel.SwitchID) string {
	if name, ok := rotaryModeName[id]; ok {
		return name
	}
	return fmt.Sprintf("mode %d", id)
}

// handleMultiSwitch dispatches a multi panel event to MechJeb airplane autopilot (AT=ARM)
// or SmartASS SAS mode selection (AT=OFF).
func handleMultiSwitch(ev multipanel.SwitchEvent, rotMode *string, ap *mechjeb.AirplaneAutopilot, smartass *mechjeb.SmartASS, atArmed bool, ctrl *spacecenter.Control) {
	if !ev.On {
		return
	}
	if !atArmed {
		handleMultiSmartASS(ev, smartass, ctrl)
		return
	}
	switch ev.Switch {
	case multipanel.RotALT:
		*rotMode = "ALT"
		log.Printf("Multi panel rotary -> ALT")
	case multipanel.RotVS:
		*rotMode = "VS"
		log.Printf("Multi panel rotary -> VS")
	case multipanel.RotIAS:
		*rotMode = "IAS"
		log.Printf("Multi panel rotary -> IAS")
	case multipanel.RotHDG:
		*rotMode = "HDG"
		log.Printf("Multi panel rotary -> HDG")
	case multipanel.RotCRS:
		*rotMode = "CRS"
		log.Printf("Multi panel rotary -> CRS")
	case multipanel.BtnAP:
		enabled, err := ap.Enabled()
		if err != nil {
			return
		}
		ap.SetEnabled(!enabled)
		log.Printf("Multi AP master: %v", !enabled)
	case multipanel.BtnHDG:
		enabled, err := ap.HeadingHoldEnabled()
		if err != nil {
			return
		}
		if !enabled {
			// Set a default bank angle of 30° if it's currently 0 (MechJeb won't turn otherwise)
			if bank, err := ap.BankAngle(); err == nil && bank == 0 {
				ap.SetBankAngle(30)
				log.Printf("Multi BankAngle defaulted to 30°")
			}
		}
		ap.SetHeadingHoldEnabled(!enabled)
		// MechJeb's EnableHeadingHold() always sets RollHoldEnabled too — roll PID is
		// what actually banks the plane; without it HeadingHold computes RealRollTarget
		// but never sends it to the flight controls.
		ap.SetRollHoldEnabled(!enabled)
		log.Printf("Multi HeadingHold: %v", !enabled)
	case multipanel.BtnALT:
		enabled, err := ap.AltitudeHoldEnabled()
		if err != nil {
			return
		}
		ap.SetAltitudeHoldEnabled(!enabled)
		log.Printf("Multi AltitudeHold: %v", !enabled)
	case multipanel.BtnVS:
		enabled, err := ap.VertSpeedHoldEnabled()
		if err != nil {
			return
		}
		if !enabled {
			// Set a default VS target of 50 m/s if it's currently 0
			if vs, err := ap.VertSpeedTarget(); err == nil && vs == 0 {
				ap.SetVertSpeedTarget(50)
				log.Printf("Multi VertSpeedTarget defaulted to 50 m/s")
			}
		}
		ap.SetVertSpeedHoldEnabled(!enabled)
		log.Printf("Multi VertSpeedHold: %v", !enabled)
	case multipanel.BtnIAS:
		enabled, err := ap.SpeedHoldEnabled()
		if err != nil {
			return
		}
		ap.SetSpeedHoldEnabled(!enabled)
		log.Printf("Multi SpeedHold: %v", !enabled)
	case multipanel.EncCW:
		adjustMultiTarget(ap, *rotMode, +1)
	case multipanel.EncCCW:
		adjustMultiTarget(ap, *rotMode, -1)
	}
}

// adjustMultiTarget nudges the MechJeb target for the given mode by one step.
// Step sizes: ALT ±100 m, VS ±1 m/s, IAS ±1 m/s, HDG ±1°, CRS ±1° bank angle.
func adjustMultiTarget(ap *mechjeb.AirplaneAutopilot, rotMode string, direction float64) {
	switch rotMode {
	case "ALT":
		current, err := ap.AltitudeTarget()
		if err != nil {
			return
		}
		next := current + direction*100
		if next < 0 {
			next = 0
		}
		ap.SetAltitudeTarget(next)
		log.Printf("Multi AltitudeTarget: %.0f m", next)
	case "VS":
		current, err := ap.VertSpeedTarget()
		if err != nil {
			return
		}
		ap.SetVertSpeedTarget(current + direction)
		log.Printf("Multi VertSpeedTarget: %.1f m/s", current+direction)
	case "IAS":
		current, err := ap.SpeedTarget()
		if err != nil {
			return
		}
		next := current + direction
		if next < 0 {
			next = 0
		}
		ap.SetSpeedTarget(next)
		log.Printf("Multi SpeedTarget: %.0f m/s", next)
	case "HDG":
		current, err := ap.HeadingTarget()
		if err != nil {
			return
		}
		next := current + direction
		for next < 0 {
			next += 360
		}
		for next >= 360 {
			next -= 360
		}
		ap.SetHeadingTarget(next)
		log.Printf("Multi HeadingTarget: %.0f°", next)
	case "CRS":
		current, err := ap.BankAngle()
		if err != nil {
			return
		}
		next := current + direction
		if next < 0 {
			next = 0
		}
		if next > 90 {
			next = 90
		}
		ap.SetBankAngle(next)
		log.Printf("Multi BankAngle: %.0f°", next)
	}
}

// syncMultiLEDs lights the button LEDs that match active MechJeb hold modes (AT=ARM)
// or the active SmartASS mode (AT=OFF).
// Returns false if any kRPC call fails.
func syncMultiLEDs(mp *multipanel.Panel, ap *mechjeb.AirplaneAutopilot, smartass *mechjeb.SmartASS, atArmed bool) bool {
	if !atArmed {
		if smartass == nil {
			mp.SetLEDs(0)
			return true
		}
		mode, err := smartass.AutopilotMode()
		if err != nil {
			return false
		}
		var leds byte
		switch mode {
		case mechjeb.SmartASSAutopilotMode_Node:
			leds = multipanel.LEDAP
		case mechjeb.SmartASSAutopilotMode_Prograde:
			leds = multipanel.LEDHDG
		case mechjeb.SmartASSAutopilotMode_Retrograde:
			leds = multipanel.LEDNAV
		case mechjeb.SmartASSAutopilotMode_NormalPlus:
			leds = multipanel.LEDIAS
		case mechjeb.SmartASSAutopilotMode_NormalMinus:
			leds = multipanel.LEDALT
		case mechjeb.SmartASSAutopilotMode_RadialPlus:
			leds = multipanel.LEDVS
		case mechjeb.SmartASSAutopilotMode_TargetPlus:
			leds = multipanel.LEDAPR
		case mechjeb.SmartASSAutopilotMode_TargetMinus:
			leds = multipanel.LEDREV
		}
		mp.SetLEDs(leds)
		return true
	}

	var leds byte
	if on, err := ap.Enabled(); err != nil {
		return false
	} else if on {
		leds |= multipanel.LEDAP
	}
	if on, err := ap.HeadingHoldEnabled(); err != nil {
		return false
	} else if on {
		leds |= multipanel.LEDHDG
	}
	if on, err := ap.AltitudeHoldEnabled(); err != nil {
		return false
	} else if on {
		leds |= multipanel.LEDALT
	}
	if on, err := ap.VertSpeedHoldEnabled(); err != nil {
		return false
	} else if on {
		leds |= multipanel.LEDVS
	}
	if on, err := ap.SpeedHoldEnabled(); err != nil {
		return false
	} else if on {
		leds |= multipanel.LEDIAS
	}
	mp.SetLEDs(leds)
	return true
}

// handleMultiSmartASS maps multi panel buttons to SmartASS SAS modes (AT=OFF).
// Pressing the active mode again turns SmartASS off.
// SAS is enabled when a mode is activated and disabled when SmartASS is turned off.
func handleMultiSmartASS(ev multipanel.SwitchEvent, smartass *mechjeb.SmartASS, ctrl *spacecenter.Control) {
	if smartass == nil {
		return
	}
	var mode mechjeb.SmartASSAutopilotMode
	var iface mechjeb.SmartASSInterfaceMode
	switch ev.Switch {
	case multipanel.BtnAP:
		mode = mechjeb.SmartASSAutopilotMode_Node
		iface = mechjeb.SmartASSInterfaceMode_Orbital
	case multipanel.BtnHDG:
		mode = mechjeb.SmartASSAutopilotMode_Prograde
		iface = mechjeb.SmartASSInterfaceMode_Orbital
	case multipanel.BtnNAV:
		mode = mechjeb.SmartASSAutopilotMode_Retrograde
		iface = mechjeb.SmartASSInterfaceMode_Orbital
	case multipanel.BtnIAS:
		mode = mechjeb.SmartASSAutopilotMode_NormalPlus
		iface = mechjeb.SmartASSInterfaceMode_Orbital
	case multipanel.BtnALT:
		mode = mechjeb.SmartASSAutopilotMode_NormalMinus
		iface = mechjeb.SmartASSInterfaceMode_Orbital
	case multipanel.BtnVS:
		mode = mechjeb.SmartASSAutopilotMode_RadialPlus
		iface = mechjeb.SmartASSInterfaceMode_Orbital
	case multipanel.BtnAPR:
		mode = mechjeb.SmartASSAutopilotMode_TargetPlus
		iface = mechjeb.SmartASSInterfaceMode_Target
	case multipanel.BtnREV:
		mode = mechjeb.SmartASSAutopilotMode_TargetMinus
		iface = mechjeb.SmartASSInterfaceMode_Target
	default:
		return
	}
	// Toggle: pressing the active mode again turns SmartASS off.
	if cur, err := smartass.AutopilotMode(); err == nil && cur == mode {
		smartass.SetAutopilotMode(mechjeb.SmartASSAutopilotMode_Off)
		smartass.Update(false)
		if ctrl != nil {
			ctrl.SetSAS(false)
		}
		log.Printf("SmartASS: Off")
		return
	}
	smartass.SetInterfaceMode(iface)
	smartass.SetAutopilotMode(mode)
	smartass.Update(false)
	if ctrl != nil {
		ctrl.SetSAS(true)
	}
	log.Printf("SmartASS: %v", mode)
}

// updateMultiDisplay writes target (Row1) and actual (Row2) for the current rotary mode.
// Returns false if any kRPC call fails.
func updateMultiDisplay(mp *multipanel.Panel, vessel *spacecenter.Vessel, ap *mechjeb.AirplaneAutopilot, rotMode string) bool {
	orbit, err := vessel.Orbit()
	if err != nil {
		return false
	}
	body, err := orbit.Body()
	if err != nil {
		return false
	}
	bodyRef, err := body.ReferenceFrame()
	if err != nil {
		return false
	}
	flight, err := vessel.Flight(bodyRef)
	if err != nil {
		return false
	}
	switch rotMode {
	case "ALT":
		target, err := ap.AltitudeTarget()
		if err != nil {
			return false
		}
		actual, err := flight.MeanAltitude()
		if err != nil {
			return false
		}
		mp.DisplayInt(multipanel.Row1, int(target))
		mp.DisplayInt(multipanel.Row2, int(actual))
	case "VS":
		target, err := ap.VertSpeedTarget()
		if err != nil {
			return false
		}
		actual, err := flight.VerticalSpeed()
		if err != nil {
			return false
		}
		mp.DisplayInt(multipanel.Row1, int(target))
		mp.DisplayInt(multipanel.Row2, int(actual))
	case "IAS":
		target, err := ap.SpeedTarget()
		if err != nil {
			return false
		}
		actual, err := flight.Speed()
		if err != nil {
			return false
		}
		mp.DisplayInt(multipanel.Row1, int(target))
		mp.DisplayInt(multipanel.Row2, int(actual))
	case "HDG":
		target, err := ap.HeadingTarget()
		if err != nil {
			return false
		}
		actual, err := flight.Heading()
		if err != nil {
			return false
		}
		mp.DisplayInt(multipanel.Row1, int(target))
		mp.DisplayInt(multipanel.Row2, int(actual))
	case "CRS":
		target, err := ap.BankAngle()
		if err != nil {
			return false
		}
		actual, err := flight.Roll()
		if err != nil {
			return false
		}
		mp.DisplayInt(multipanel.Row1, int(target))
		mp.DisplayInt(multipanel.Row2, int(actual))
	}
	return true
}
