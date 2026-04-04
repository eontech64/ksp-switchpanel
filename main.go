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
	log.Printf("KSP panels bridge v%s starting...", version)

	cfg, err := config.FindAndLoad()
	if err != nil {
		log.Fatalf("Config: %v", err)
	}
	log.Println("Configuration loaded.")

	log.Println("Connecting to Switch Panel...")
	swPanel, err := switchpanel.Open()
	if err != nil {
		log.Printf("Switch panel not found, continuing without it: %v", err)
		swPanel = nil
	} else {
		log.Println("Switch panel connected.")
		defer swPanel.Close()
	}

	log.Println("Connecting to Radio Panel...")
	radioPanel, err := radiopanel.Open()
	if err != nil {
		log.Printf("Radio panel not found, continuing without it: %v", err)
		radioPanel = nil
	} else {
		log.Println("Radio panel connected.")
		defer radioPanel.Close()
	}

	if swPanel == nil && radioPanel == nil {
		log.Fatal("No panels found. Connect at least one panel and try again.")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var swCh <-chan switchpanel.SwitchEvent
	if swPanel != nil {
		swCh = swPanel.SwitchCh()
	}
	var radioCh <-chan radiopanel.SwitchEvent
	if radioPanel != nil {
		radioCh = radioPanel.SwitchCh()
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// rot modes persist across missions — they reflect the physical rotary position.
	rot1Mode := "COM1"
	rot2Mode := "COM1"

	kRPCCfg := krpcgo.KRPCClientConfig{
		ClientName: "KSP-Panels",
		RPCOnly:    true,
	}

	// Outer loop: reconnects kRPC + vessel on each new mission.
	for {
		// Each iteration creates a fresh kRPC client so a dropped TCP connection
		// is fully recovered when KSP starts a new mission.
		ctx, cancelCtx := context.WithCancel(context.Background())

		log.Println("Connecting to kRPC server...")
		client := krpcgo.NewKRPCClient(kRPCCfg)
		if err := client.Connect(ctx); err != nil {
			log.Printf("kRPC connect failed: %v — retrying in 5s...", err)
			cancelCtx()
			time.Sleep(5 * time.Second)
			continue
		}
		log.Println("kRPC connected.")

		sc := spacecenter.New(client)
		vessel, control := waitForVessel(sc, swPanel)

		log.Println("Ready.")
		vesselLost := runSession(ctx, vessel, control, swPanel, radioPanel, cfg,
			swCh, radioCh, ticker.C, sigCh, &rot1Mode, &rot2Mode)

		client.Close()
		cancelCtx()

		if !vesselLost {
			// Clean shutdown via signal.
			log.Println("Shutting down...")
			if swPanel != nil {
				swPanel.SetLEDs(0)
			}
			return
		}
		log.Println("Session ended. Reconnecting...")
		time.Sleep(2 * time.Second)
	}
}

// waitForVessel blocks until an active vessel is available in KSP.
func waitForVessel(sc *spacecenter.SpaceCenter, swPanel *switchpanel.Panel) (*spacecenter.Vessel, *spacecenter.Control) {
	log.Println("Waiting for active vessel...")
	for {
		vessel, err := sc.ActiveVessel()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		control, err := vessel.Control()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		name, _ := vessel.Name()
		log.Printf("Active vessel: %s", name)
		if swPanel != nil {
			swPanel.SetLEDs(0)
		}
		return vessel, control
	}
}

// runSession runs the main event loop for a single active vessel.
// Returns true if the vessel was lost (new mission), false on clean shutdown.
func runSession(
	ctx context.Context,
	vessel *spacecenter.Vessel,
	control *spacecenter.Control,
	swPanel *switchpanel.Panel,
	radioPanel *radiopanel.Panel,
	cfg *config.Config,
	swCh <-chan switchpanel.SwitchEvent,
	radioCh <-chan radiopanel.SwitchEvent,
	tickC <-chan time.Time,
	sigCh <-chan os.Signal,
	rot1Mode, rot2Mode *string,
) bool {
	errCount := 0
	const maxErrors = 3

	for {
		select {
		case <-ctx.Done():
			return false
		case <-sigCh:
			return false

		case ev := <-swCh:
			handleSwitch(ev, cfg.Switches, control)

		case ev := <-radioCh:
			handleRadioSwitch(ev, rot1Mode, rot2Mode)

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

// syncLEDs updates the landing gear LEDs to reflect the current gear state.
// Returns false if the vessel reference appears to be invalid.
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

func radioModeName(id radiopanel.SwitchID) string {
	if name, ok := rotaryModeName[id]; ok {
		return name
	}
	return fmt.Sprintf("mode %d", id)
}
