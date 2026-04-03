package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	krpcgo "github.com/atburke/krpc-go"
	"github.com/atburke/krpc-go/spacecenter"

	"ksp-switchpanel/radiopanel"
	"ksp-switchpanel/switchpanel"
)

func main() {
	log.Printf("KSP panels bridge v%s starting...", version)

	// Connect to the switch panel (optional)
	log.Println("Connecting to Switch Panel...")
	swPanel, err := switchpanel.Open()
	if err != nil {
		log.Printf("Switch panel not found, continuing without it: %v", err)
		swPanel = nil
	} else {
		log.Println("Switch panel connected.")
		defer swPanel.Close()
	}

	// Connect to the radio panel (optional)
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

	// Connect to kRPC
	log.Println("Connecting to kRPC server...")
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	client := krpcgo.NewKRPCClient(krpcgo.KRPCClientConfig{
		ClientName: "KSP-Panels",
		RPCOnly:    true,
	})
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("kRPC: %v", err)
	}
	defer client.Close()
	log.Println("kRPC connected.")

	sc := spacecenter.New(client)
	log.Println("Waiting for active vessel...")
	var vessel *spacecenter.Vessel
	for {
		vessel, err = sc.ActiveVessel()
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	name, _ := vessel.Name()
	log.Printf("Active vessel: %s", name)

	control, err := vessel.Control()
	if err != nil {
		log.Fatalf("Control: %v", err)
	}

	if swPanel != nil {
		swPanel.SetLEDs(0)
	}

	// Handle OS signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var swCh <-chan switchpanel.SwitchEvent
	if swPanel != nil {
		swCh = swPanel.SwitchCh()
	}

	// Radio panel state: which telemetry mode each rotary is set to
	rot1Mode := radiopanel.Rot1COM1 // top displays
	rot2Mode := radiopanel.Rot2COM1 // bottom displays
	var radioCh <-chan radiopanel.SwitchEvent
	if radioPanel != nil {
		radioCh = radioPanel.SwitchCh()
	}

	log.Println("Ready.")

	// Telemetry ticker: update radio panel displays
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			log.Println("Shutting down...")
			if swPanel != nil {
				swPanel.SetLEDs(0)
			}
			cancelCtx()
			return

		case ev := <-swCh:
			handleSwitch(ev, control, swPanel)

		case ev := <-radioCh:
			handleRadioSwitch(ev, &rot1Mode, &rot2Mode)

		case <-ticker.C:
			if swPanel != nil {
				syncLEDs(control, swPanel)
			}
			if radioPanel != nil {
				updateDisplays(radioPanel, vessel, control, rot1Mode, rot2Mode)
			}
		}
	}
}

// handleSwitch maps switch panel events to kRPC actions.
func handleSwitch(ev switchpanel.SwitchEvent, ctrl *spacecenter.Control, panel *switchpanel.Panel) {
	on := ev.On
	switch ev.Switch {
	case switchpanel.SwBat:
		log.Printf("BAT -> RCS: %v", on)
		ctrl.SetRCS(on)
	case switchpanel.SwAlternator:
		log.Printf("ALT -> SAS: %v", on)
		ctrl.SetSAS(on)
	case switchpanel.SwAvionics:
		ctrl.SetActionGroup(1, on)
	case switchpanel.SwFuel:
		ctrl.SetActionGroup(2, on)
	case switchpanel.SwDeice:
		ctrl.SetActionGroup(3, on)
	case switchpanel.SwPitot:
		ctrl.SetActionGroup(4, on)
	case switchpanel.SwCowl:
		ctrl.SetActionGroup(5, on)
	case switchpanel.SwPanel:
		ctrl.SetActionGroup(6, on)
	case switchpanel.SwBeacon:
		ctrl.SetActionGroup(7, on)
	case switchpanel.SwNav:
		ctrl.SetActionGroup(8, on)
	case switchpanel.SwStrobe:
		ctrl.SetActionGroup(9, on)
	case switchpanel.SwTaxi:
		ctrl.SetActionGroup(10, on)
	case switchpanel.SwLanding:
		log.Printf("LANDING -> Brakes: %v", on)
		ctrl.SetBrakes(on)
	case switchpanel.GearDown:
		if on {
			log.Println("GEAR DOWN -> Deploy gear")
			ctrl.SetGear(true)
		}
	case switchpanel.GearUp:
		if on {
			log.Println("GEAR UP -> Retract gear")
			ctrl.SetGear(false)
		}
	case switchpanel.RotStart:
		if on {
			log.Println("ROT START -> Next stage!")
			ctrl.ActivateNextStage()
		}
	}
}

// handleRadioSwitch tracks rotary position changes to update display modes.
func handleRadioSwitch(ev radiopanel.SwitchEvent, rot1Mode, rot2Mode *radiopanel.SwitchID) {
	if !ev.On {
		return
	}
	switch ev.Switch {
	case radiopanel.Rot1COM1, radiopanel.Rot1COM2, radiopanel.Rot1NAV1,
		radiopanel.Rot1NAV2, radiopanel.Rot1ADF, radiopanel.Rot1DME, radiopanel.Rot1XPDR:
		*rot1Mode = ev.Switch
		log.Printf("Radio top mode: %s", radioModeName(ev.Switch))
	case radiopanel.Rot2COM1, radiopanel.Rot2COM2, radiopanel.Rot2NAV1,
		radiopanel.Rot2NAV2, radiopanel.Rot2ADF, radiopanel.Rot2DME, radiopanel.Rot2XPDR:
		*rot2Mode = ev.Switch
		log.Printf("Radio bottom mode: %s", radioModeName(ev.Switch))
	}
}

// updateDisplays writes telemetry to the radio panel based on current rotary modes.
func updateDisplays(rp *radiopanel.Panel, vessel *spacecenter.Vessel, ctrl *spacecenter.Control, rot1Mode, rot2Mode radiopanel.SwitchID) {
	orbit, err := vessel.Orbit()
	if err != nil {
		return
	}
	body, err := orbit.Body()
	if err != nil {
		return
	}
	bodyRef, err := body.ReferenceFrame()
	if err != nil {
		return
	}
	flight, err := vessel.Flight(bodyRef)
	if err != nil {
		return
	}

	// Match the navball speed: surface or orbital depending on current mode.
	var navSpeed float64
	if speedMode, err := ctrl.SpeedMode(); err == nil {
		if speedMode == spacecenter.SpeedMode_Orbit {
			navSpeed, _ = orbit.Speed()
		} else {
			navSpeed, _ = flight.Speed()
		}
	}

	writeModePair(rp, flight, orbit, navSpeed, rot1Mode, radiopanel.Display1Active, radiopanel.Display1Standby)
	writeModePair(rp, flight, orbit, navSpeed, rot2Mode, radiopanel.Display2Active, radiopanel.Display2Standby)
}

// writeModePair writes the left/right value pair for a given mode onto the two displays.
func writeModePair(rp *radiopanel.Panel, flight *spacecenter.Flight, orbit *spacecenter.Orbit,
	navSpeed float64, mode radiopanel.SwitchID, left, right radiopanel.DisplayID) {

	switch mode {
	case radiopanel.Rot1COM1, radiopanel.Rot2COM1:
		// Altitude (km, 1 decimal) | Vertical speed (m/s)
		if v, err := flight.MeanAltitude(); err == nil {
			rp.DisplayFloat(left, v/1000, 1)
		}
		if v, err := flight.VerticalSpeed(); err == nil {
			rp.DisplayInt(right, int(v))
		}

	case radiopanel.Rot1COM2, radiopanel.Rot2COM2:
		// Apoapsis (km) | Periapsis (km)
		if v, err := orbit.ApoapsisAltitude(); err == nil {
			rp.DisplayInt(left, int(v/1000))
		}
		if v, err := orbit.PeriapsisAltitude(); err == nil {
			rp.DisplayInt(right, int(v/1000))
		}

	case radiopanel.Rot1NAV1, radiopanel.Rot2NAV1:
		// Speed (knots, navball) | Heading (°)
		rp.DisplayInt(left, int(navSpeed*1.94384))
		if v, err := flight.Heading(); err == nil {
			rp.DisplayInt(right, int(v))
		}

	case radiopanel.Rot1NAV2, radiopanel.Rot2NAV2:
		// Pitch (°) | Roll (°)
		if v, err := flight.Pitch(); err == nil {
			rp.DisplayFloat(left, float64(v), 1)
		}
		if v, err := flight.Roll(); err == nil {
			rp.DisplayFloat(right, float64(v), 1)
		}

	case radiopanel.Rot1ADF, radiopanel.Rot2ADF:
		// Latitude | Longitude
		if v, err := flight.Latitude(); err == nil {
			rp.DisplayFloat(left, v, 2)
		}
		if v, err := flight.Longitude(); err == nil {
			rp.DisplayFloat(right, v, 2)
		}

	case radiopanel.Rot1DME, radiopanel.Rot2DME:
		// G-Force | Dynamic pressure (Pa)
		if v, err := flight.GForce(); err == nil {
			rp.DisplayFloat(left, float64(v), 2)
		}
		if v, err := flight.DynamicPressure(); err == nil {
			rp.DisplayInt(right, int(v))
		}

	case radiopanel.Rot1XPDR, radiopanel.Rot2XPDR:
		// Time to apoapsis (s) | Time to periapsis (s)
		if v, err := orbit.TimeToApoapsis(); err == nil {
			rp.DisplayInt(left, int(math.Abs(v)))
		}
		if v, err := orbit.TimeToPeriapsis(); err == nil {
			rp.DisplayInt(right, int(math.Abs(v)))
		}
	}
}

func radioModeName(id radiopanel.SwitchID) string {
	names := map[radiopanel.SwitchID]string{
		radiopanel.Rot1COM1: "Alt (km) / VSpeed (m/s)",
		radiopanel.Rot1COM2: "Apo / Peri (km)",
		radiopanel.Rot1NAV1: "Speed (kts, navball) / Heading",
		radiopanel.Rot1NAV2: "Pitch / Roll",
		radiopanel.Rot1ADF:  "Lat / Lon",
		radiopanel.Rot1DME:  "G-Force / DynPres",
		radiopanel.Rot1XPDR: "T-Apo / T-Peri",
		radiopanel.Rot2COM1: "Alt (km) / VSpeed (m/s)",
		radiopanel.Rot2COM2: "Apo / Peri (km)",
		radiopanel.Rot2NAV1: "Speed (kts, navball) / Heading",
		radiopanel.Rot2NAV2: "Pitch / Roll",
		radiopanel.Rot2ADF:  "Lat / Lon",
		radiopanel.Rot2DME:  "G-Force / DynPres",
		radiopanel.Rot2XPDR: "T-Apo / T-Peri",
	}
	if s, ok := names[id]; ok {
		return s
	}
	return fmt.Sprintf("mode %d", id)
}

func syncLEDs(ctrl *spacecenter.Control, panel *switchpanel.Panel) {
	deployed, err := ctrl.Gear()
	if err != nil {
		return
	}
	if deployed {
		panel.SetLEDs(switchpanel.LEDAllGreen)
	} else {
		panel.SetLEDs(switchpanel.LEDAllRed)
	}
}
