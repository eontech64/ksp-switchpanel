package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
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
// Both rows share the same mode names (COM1, NAV1, etc.).
var rotaryModeName = map[radiopanel.SwitchID]string{
	radiopanel.Rot1COM1: "COM1", radiopanel.Rot2COM1: "COM1",
	radiopanel.Rot1COM2: "COM2", radiopanel.Rot2COM2: "COM2",
	radiopanel.Rot1NAV1: "NAV1", radiopanel.Rot2NAV1: "NAV1",
	radiopanel.Rot1NAV2: "NAV2", radiopanel.Rot2NAV2: "NAV2",
	radiopanel.Rot1ADF:  "ADF",  radiopanel.Rot2ADF:  "ADF",
	radiopanel.Rot1DME:  "DME",  radiopanel.Rot2DME:  "DME",
	radiopanel.Rot1XPDR: "XPDR", radiopanel.Rot2XPDR: "XPDR",
}

// rot1Switch is true for SwitchIDs that belong to the top radio row.
var rot1Switch = map[radiopanel.SwitchID]bool{
	radiopanel.Rot1COM1: true, radiopanel.Rot1COM2: true,
	radiopanel.Rot1NAV1: true, radiopanel.Rot1NAV2: true,
	radiopanel.Rot1ADF:  true, radiopanel.Rot1DME:  true,
	radiopanel.Rot1XPDR: true,
}

// telemetryFn is a function that reads one telemetry value from the current flight state.
type telemetryFn func(f *spacecenter.Flight, o *spacecenter.Orbit, navSpeed float64) (float64, error)

// telemetryFns is the registry of field names available in the [radio] config.
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

func main() {
	log.Printf("KSP panels bridge v%s starting...", appVersion)

	cfg, err := config.FindAndLoad()
	if err != nil {
		log.Fatalf("Config: %v", err)
	}
	log.Println("Configuration loaded.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Rotary modes persist across missions — they reflect the physical rotary position.
	rot1Mode := "COM1"
	rot2Mode := "COM1"
	// Multi panel rotary: starts at ALT; overridden by initial-state event on startup.
	multiRotMode := "ALT"

	kRPCCfg := krpcgo.KRPCClientConfig{
		ClientName: "KSP-Panels",
		RPCOnly:    true,
	}

	var swPanel *switchpanel.Panel
	var radioPanel *radiopanel.Panel
	var multiPanel *multipanel.Panel

	// Outer loop: reconnects kRPC + vessel on each new mission.
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

		if swPanel == nil && radioPanel == nil && multiPanel == nil {
			log.Println("No panels found. Waiting for a panel to be connected...")
			select {
			case <-sigCh:
				log.Println("Shutting down.")
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// Derive channels fresh each iteration so reconnected panels get their new channels.
		var swCh <-chan switchpanel.SwitchEvent
		if swPanel != nil {
			swCh = swPanel.SwitchCh()
		}
		var radioCh <-chan radiopanel.SwitchEvent
		if radioPanel != nil {
			radioCh = radioPanel.SwitchCh()
		}
		var multiCh <-chan multipanel.SwitchEvent
		if multiPanel != nil {
			multiCh = multiPanel.SwitchCh()
		}

		// Each iteration creates a fresh kRPC client so a dropped TCP connection
		// is fully recovered when KSP starts a new mission.
		ctx, cancelCtx := context.WithCancel(context.Background())

		log.Println("Connecting to kRPC server...")
		client := krpcgo.NewKRPCClient(kRPCCfg)
		if err := client.Connect(ctx); err != nil {
			log.Printf("kRPC connect failed: %v — retrying in 1s...", err)
			cancelCtx()
			select {
			case <-sigCh:
				log.Println("Shutting down.")
				shutdownPanels(swPanel, radioPanel, multiPanel)
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}
		log.Println("kRPC connected.")

		sc := spacecenter.New(client)
		vessel, control, ok := waitForVessel(sc, swPanel, sigCh)
		if !ok {
			client.Close()
			cancelCtx()
			log.Println("Shutting down.")
			shutdownPanels(swPanel, radioPanel, multiPanel)
			return
		}

		mj := mechjeb.New(client)
		var ap *mechjeb.AirplaneAutopilot
		if ready, err := mj.APIReady(); err != nil {
			log.Printf("MechJeb service not available (is KRPC.MechJeb installed?): %v", err)
		} else if !ready {
			log.Println("MechJeb service found but APIReady=false (MechJeb not loaded yet?)")
		} else {
			log.Println("MechJeb APIReady=true, getting AirplaneAutopilot...")
			if a, err := mj.AirplaneAutopilot(); err != nil {
				log.Printf("MechJeb AirplaneAutopilot unavailable: %v", err)
			} else {
				ap = a
				log.Println("MechJeb AirplaneAutopilot ready.")
			}
		}

		log.Println("Ready.")
		vesselLost := runSession(ctx, vessel, control, ap, swPanel, radioPanel, multiPanel, cfg,
			swCh, radioCh, multiCh, ticker.C, sigCh, &rot1Mode, &rot2Mode, &multiRotMode)

		client.Close()
		cancelCtx()

		if !vesselLost {
			log.Println("Shutting down.")
			shutdownPanels(swPanel, radioPanel, multiPanel)
			return
		}

		// Session ended (vessel lost or panel disconnected) — blank what's still connected.
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
		case <-sigCh:
			log.Println("Shutting down.")
			shutdownPanels(swPanel, radioPanel, multiPanel)
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// shutdownPanels blanks outputs and closes all open panels.
func shutdownPanels(swPanel *switchpanel.Panel, radioPanel *radiopanel.Panel, multiPanel *multipanel.Panel) {
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

// waitForVessel blocks until an active vessel is available in KSP.
// Returns (vessel, control, true) on success, or (nil, nil, false) if a shutdown signal is received.
func waitForVessel(sc *spacecenter.SpaceCenter, swPanel *switchpanel.Panel, sigCh <-chan os.Signal) (*spacecenter.Vessel, *spacecenter.Control, bool) {
	log.Println("Waiting for active vessel...")
	for {
		vessel, err := sc.ActiveVessel()
		if err != nil {
			select {
			case <-sigCh:
				return nil, nil, false
			case <-time.After(2 * time.Second):
			}
			continue
		}
		control, err := vessel.Control()
		if err != nil {
			select {
			case <-sigCh:
				return nil, nil, false
			case <-time.After(2 * time.Second):
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

// runSession runs the main event loop for a single active vessel.
// Returns true if the vessel was lost or a panel disconnected (retry), false on clean shutdown.
func runSession(
	ctx context.Context,
	vessel *spacecenter.Vessel,
	control *spacecenter.Control,
	ap *mechjeb.AirplaneAutopilot,
	swPanel *switchpanel.Panel,
	radioPanel *radiopanel.Panel,
	multiPanel *multipanel.Panel,
	cfg *config.Config,
	swCh <-chan switchpanel.SwitchEvent,
	radioCh <-chan radiopanel.SwitchEvent,
	multiCh <-chan multipanel.SwitchEvent,
	tickC <-chan time.Time,
	sigCh <-chan os.Signal,
	rot1Mode, rot2Mode *string,
	multiRotMode *string,
) bool {
	errCount := 0
	const maxErrors = 3
	gearState := &gearLEDState{}

	// Done channels — nil channel never fires, so absent panels are handled safely.
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
		case <-sigCh:
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
			handleSwitch(ev, cfg.Switches, control)

		case ev := <-radioCh:
			handleRadioSwitch(ev, rot1Mode, rot2Mode)
			if ap != nil {
				handleAutopilot(ev, ap)
			}

		case ev := <-multiCh:
			if ap != nil {
				handleMultiSwitch(ev, multiRotMode, ap, control)
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
				if !syncMultiLEDs(multiPanel, ap) || !updateMultiDisplay(multiPanel, vessel, ap, *multiRotMode) {
					tickOK = false
				}
			}
			if !tickOK {
				errCount++
				if errCount >= maxErrors {
					return true // vessel lost
				}
			} else {
				errCount = 0
			}
		}
	}
}

// handleSwitch dispatches a switch event to the corresponding kRPC action from config.
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
		log.Printf("%s -> RCS: %v", name, on)
		ctrl.SetRCS(on)
	case action == "sas":
		log.Printf("%s -> SAS: %v", name, on)
		ctrl.SetSAS(on)
	case action == "brakes":
		log.Printf("%s -> Brakes: %v", name, on)
		ctrl.SetBrakes(on)
	case action == "gear_down" && on:
		log.Printf("%s -> Gear DOWN", name)
		ctrl.SetGear(true)
	case action == "gear_up" && on:
		log.Printf("%s -> Gear UP", name)
		ctrl.SetGear(false)
	case action == "next_stage" && on:
		log.Printf("%s -> Next stage", name)
		ctrl.ActivateNextStage()
	case strings.HasPrefix(action, "action_group:"):
		n, err := strconv.Atoi(strings.TrimPrefix(action, "action_group:"))
		if err != nil {
			log.Printf("Invalid action_group in config for %s: %q", name, action)
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
			log.Printf("%s -> SAS mode: %s", name, strings.TrimPrefix(action, "sas_mode:"))
			ctrl.SetSASMode(m)
		}
	}
}

// handleRadioSwitch tracks which telemetry mode each rotary row is set to.
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

// updateDisplays writes telemetry to the radio panel based on current rotary modes.
// Returns false if the vessel reference appears to be invalid.
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

// writeDisplayPair writes the left/right telemetry values for a given mode onto two displays.
func writeDisplayPair(rp *radiopanel.Panel, flight *spacecenter.Flight, orbit *spacecenter.Orbit,
	navSpeed float64, radioCfg map[string][]string, mode string,
	left, right radiopanel.DisplayID) {

	specs, ok := radioCfg[mode]
	if !ok || len(specs) < 2 {
		return
	}
	leftSpec, err := config.ParseDisplaySpec(specs[0])
	if err != nil {
		log.Printf("Radio config [%s] left: %v", mode, err)
		return
	}
	rightSpec, err := config.ParseDisplaySpec(specs[1])
	if err != nil {
		log.Printf("Radio config [%s] right: %v", mode, err)
		return
	}
	writeDisplay(rp, left, leftSpec, flight, orbit, navSpeed)
	writeDisplay(rp, right, rightSpec, flight, orbit, navSpeed)
}

// writeDisplay reads one telemetry field and writes it to a single display.
func writeDisplay(rp *radiopanel.Panel, display radiopanel.DisplayID, spec config.DisplaySpec,
	flight *spacecenter.Flight, orbit *spacecenter.Orbit, navSpeed float64) {

	fn, ok := telemetryFns[spec.Field]
	if !ok {
		log.Printf("Unknown telemetry field %q", spec.Field)
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
		// First call: record state without triggering transition.
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

func radioModeName(id radiopanel.SwitchID) string {
	if name, ok := rotaryModeName[id]; ok {
		return name
	}
	return fmt.Sprintf("mode %d", id)
}

// handleAutopilot maps radio panel encoder and button events to MechJeb airplane autopilot.
//
//	SwAct1        — toggle speed hold
//	SwAct2        — toggle altitude hold
//	Enc1Inner     — speed target ±1 kt
//	Enc1Outer     — speed target ±10 kt
//	Enc2Inner     — altitude target ±10 m
//	Enc2Outer     — altitude target ±100 m
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

// handleMultiSwitch dispatches a multi panel event to MechJeb airplane autopilot.
//
// Rotary selector (ALT/VS/IAS/HDG/CRS) — sets the active mode tracked by rotMode.
// Buttons AP/HDG/ALT/VS/IAS — toggle the corresponding MechJeb hold.
// Encoder CW/CCW — nudge the target value for the current mode:
//
//	ALT: ±100 m   VS: ±1 m/s   IAS: ±1 m/s   HDG/CRS: ±1°
func handleMultiSwitch(ev multipanel.SwitchEvent, rotMode *string, ap *mechjeb.AirplaneAutopilot, ctrl *spacecenter.Control) {
	if !ev.On {
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
		ap.SetHeadingHoldEnabled(!enabled)
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

// adjustMultiTarget nudges the MechJeb target value for the given mode by delta steps.
// Step sizes: ALT ±100 m, VS ±1 m/s, IAS ±1 m/s, HDG/CRS ±1°.
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
		next := current + direction*1
		ap.SetVertSpeedTarget(next)
		log.Printf("Multi VertSpeedTarget: %.1f m/s", next)

	case "IAS":
		current, err := ap.SpeedTarget()
		if err != nil {
			return
		}
		next := current + direction*1
		if next < 0 {
			next = 0
		}
		ap.SetSpeedTarget(next)
		log.Printf("Multi SpeedTarget: %.0f m/s", next)

	case "HDG", "CRS":
		current, err := ap.HeadingTarget()
		if err != nil {
			return
		}
		next := current + direction*1
		for next < 0 {
			next += 360
		}
		for next >= 360 {
			next -= 360
		}
		ap.SetHeadingTarget(next)
		log.Printf("Multi HeadingTarget: %.0f°", next)
	}
}

// syncMultiLEDs lights the button LEDs that match active MechJeb hold modes.
// Returns false if any kRPC call fails (vessel likely lost).
func syncMultiLEDs(mp *multipanel.Panel, ap *mechjeb.AirplaneAutopilot) bool {
	var leds byte

	masterOn, err := ap.Enabled()
	if err != nil {
		return false
	}
	if masterOn {
		leds |= multipanel.LEDAP
	}

	hdg, err := ap.HeadingHoldEnabled()
	if err != nil {
		return false
	}
	if hdg {
		leds |= multipanel.LEDHDG
	}

	alt, err := ap.AltitudeHoldEnabled()
	if err != nil {
		return false
	}
	if alt {
		leds |= multipanel.LEDALT
	}

	vs, err := ap.VertSpeedHoldEnabled()
	if err != nil {
		return false
	}
	if vs {
		leds |= multipanel.LEDVS
	}

	ias, err := ap.SpeedHoldEnabled()
	if err != nil {
		return false
	}
	if ias {
		leds |= multipanel.LEDIAS
	}

	mp.SetLEDs(leds)
	return true
}

// updateMultiDisplay writes the target (Row1) and actual (Row2) values for the
// current rotary mode onto the multi panel segment display.
// Returns false if any kRPC call fails (vessel likely lost).
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

	case "HDG", "CRS":
		target, err := ap.HeadingTarget()
		if err != nil {
			return false
		}
		actualRaw, err := flight.Heading()
		if err != nil {
			return false
		}
		mp.DisplayInt(multipanel.Row1, int(target))
		mp.DisplayInt(multipanel.Row2, int(actualRaw))
	}
	return true
}
