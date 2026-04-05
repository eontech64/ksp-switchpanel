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
// It sends status updates on statusCh whenever the connection state changes.
// statusCh is closed when RunBridge returns.
func RunBridge(
	statusCh chan<- BridgeStatus,
	quitCh <-chan struct{},
	cfg *config.Config,
	swPanel *switchpanel.Panel,
	radioPanel *radiopanel.Panel,
) {
	defer close(statusCh)

	send := func(s BridgeStatus) {
		select {
		case statusCh <- s:
		default:
		}
	}

	hasSW := swPanel != nil
	hasRD := radioPanel != nil

	var swCh <-chan switchpanel.SwitchEvent
	if hasSW {
		swCh = swPanel.SwitchCh()
	}
	var radioCh <-chan radiopanel.SwitchEvent
	if hasRD {
		radioCh = radioPanel.SwitchCh()
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	rot1Mode := "COM1"
	rot2Mode := "COM1"

	// switchPos tracks the physical position of each switch across sessions.
	// When reconnecting, these are re-applied to the new vessel.
	switchPos := make(map[switchpanel.SwitchID]bool)

	kRPCCfg := krpcgo.KRPCClientConfig{
		ClientName: "KSP-Panels",
		RPCOnly:    true,
	}

	for {
		send(BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, KRPC: "connecting"})

		ctx, cancelCtx := context.WithCancel(context.Background())

		log.Println("Connecting to kRPC server...")
		client := krpcgo.NewKRPCClient(kRPCCfg)
		if err := client.Connect(ctx); err != nil {
			log.Printf("kRPC connect failed: %v — retrying in 1s...", err)
			cancelCtx()
			select {
			case <-quitCh:
				cleanup(swPanel, radioPanel)
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}
		log.Println("kRPC connected.")
		send(BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, KRPC: "connected"})

		sc := spacecenter.New(client)
		vessel, control, ok := waitForVessel(sc, swPanel, quitCh)
		if !ok {
			client.Close()
			cancelCtx()
			cleanup(swPanel, radioPanel)
			return
		}

		// Re-apply remembered switch positions to the new vessel.
		reapplySwitches(switchPos, cfg.Switches, control)

		mj := mechjeb.New(client)
		ap, err := mj.AirplaneAutopilot()
		if err != nil {
			ap = nil
		} else {
			log.Println("MechJeb AirplaneAutopilot ready.")
		}

		name, _ := vessel.Name()
		base := BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, KRPC: "connected", VesselName: name, MechJeb: ap != nil}
		send(base)

		log.Println("Ready.")
		vesselLost := runSession(ctx, vessel, control, ap, swPanel, radioPanel, cfg,
			swCh, radioCh, ticker.C, quitCh, &rot1Mode, &rot2Mode,
			statusCh, base, switchPos)

		client.Close()
		cancelCtx()

		if !vesselLost {
			cleanup(swPanel, radioPanel)
			return
		}

		// Vessel lost — blank panels and retry.
		if swPanel != nil {
			swPanel.SetLEDs(0)
		}
		if radioPanel != nil {
			radioPanel.DisplayOff()
		}
		log.Println("Session ended. Reconnecting...")
		send(BridgeStatus{SwitchPanel: hasSW, RadioPanel: hasRD, KRPC: "connecting"})
		select {
		case <-quitCh:
			cleanup(swPanel, radioPanel)
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func cleanup(swPanel *switchpanel.Panel, radioPanel *radiopanel.Panel) {
	if swPanel != nil {
		swPanel.SetLEDs(0)
	}
	if radioPanel != nil {
		radioPanel.DisplayOff()
	}
}

func waitForVessel(sc *spacecenter.SpaceCenter, swPanel *switchpanel.Panel, quitCh <-chan struct{}) (*spacecenter.Vessel, *spacecenter.Control, bool) {
	log.Println("Waiting for active vessel...")
	for {
		vessel, err := sc.ActiveVessel()
		if err != nil {
			select {
			case <-quitCh:
				return nil, nil, false
			case <-time.After(1 * time.Second):
			}
			continue
		}
		control, err := vessel.Control()
		if err != nil {
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
	swPanel *switchpanel.Panel,
	radioPanel *radiopanel.Panel,
	cfg *config.Config,
	swCh <-chan switchpanel.SwitchEvent,
	radioCh <-chan radiopanel.SwitchEvent,
	tickC <-chan time.Time,
	quitCh <-chan struct{},
	rot1Mode, rot2Mode *string,
	statusCh chan<- BridgeStatus,
	base BridgeStatus,
	switchPos map[switchpanel.SwitchID]bool,
) bool {
	errCount := 0
	const maxErrors = 3

	sendStatus := func(s BridgeStatus) {
		select {
		case statusCh <- s:
		default:
		}
	}

	lastAP := BridgeStatus{}

	for {
		select {
		case <-ctx.Done():
			return false
		case <-quitCh:
			return false
		case ev := <-swCh:
			switchPos[ev.Switch] = ev.On
			handleSwitch(ev, cfg.Switches, control)
		case ev := <-radioCh:
			handleRadioSwitch(ev, rot1Mode, rot2Mode)
			if ap != nil {
				handleAutopilot(ev, ap)
			}
		case <-tickC:
			tickOK := true
			if swPanel != nil && !syncLEDs(control, swPanel) {
				tickOK = false
			}
			if radioPanel != nil && !updateDisplays(radioPanel, vessel, control, *rot1Mode, *rot2Mode, cfg.Radio) {
				tickOK = false
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
		ctrl.SetActionGroup(uint32(n), on)
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

func syncLEDs(ctrl *spacecenter.Control, panel *switchpanel.Panel) bool {
	deployed, err := ctrl.Gear()
	if err != nil {
		return false
	}
	if deployed {
		panel.SetLEDs(switchpanel.LEDAllGreen)
	} else {
		panel.SetLEDs(switchpanel.LEDAllRed)
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
