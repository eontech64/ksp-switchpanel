package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	krpcgo "github.com/atburke/krpc-go"
	"github.com/atburke/krpc-go/spacecenter"

	"ksp-switchpanel/switchpanel"
)

func main() {
	log.Println("KSP Switch Panel bridge starting...")

	// Connect to the switch panel via hidapi (IOKit on macOS)
	log.Println("Connecting to Saitek Switch Panel...")
	panel, err := switchpanel.Open()
	if err != nil {
		log.Fatalf("Failed to connect to switch panel (is it plugged in?): %v", err)
	}
	defer panel.Close()
	log.Println("Switch panel connected.")

	// Connect to kRPC (KSP must be running with kRPC server active)
	log.Println("Connecting to kRPC server...")
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	client := krpcgo.NewKRPCClient(krpcgo.KRPCClientConfig{
		ClientName: "KSP-SwitchPanel",
	})
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to kRPC: %v", err)
	}
	defer client.Close()
	log.Println("kRPC connected.")

	sc := spacecenter.New(client)
	log.Println("Waiting for active vessel (load a ship in KSP)...")
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
		log.Fatalf("Failed to get vessel control: %v", err)
	}

	// Turn off all LEDs at startup
	panel.SetLEDs(0)

	// Handle OS signals for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	switchCh := panel.SwitchCh()

	log.Println("Ready. Switch mapping:")
	log.Println("  BAT       -> RCS")
	log.Println("  ALT       -> SAS")
	log.Println("  AVIONICS  -> Action Group 1")
	log.Println("  FUEL      -> Action Group 2")
	log.Println("  DE-ICE    -> Action Group 3")
	log.Println("  PITOT     -> Action Group 4")
	log.Println("  COWL      -> Action Group 5")
	log.Println("  PANEL     -> Action Group 6")
	log.Println("  BEACON    -> Action Group 7")
	log.Println("  NAV       -> Action Group 8")
	log.Println("  STROBE    -> Action Group 9")
	log.Println("  TAXI      -> Action Group 10")
	log.Println("  LANDING   -> Brakes")
	log.Println("  GEAR DOWN -> Deploy gear")
	log.Println("  GEAR UP   -> Retract gear")
	log.Println("  ROT START -> Activate next stage")

	// Polling ticker to sync landing gear LEDs with vessel state
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			log.Println("Shutting down...")
			panel.SetLEDs(0)
			cancelCtx()
			return

		case ev := <-switchCh:
			handleSwitch(ev, control, panel)

		case <-ticker.C:
			syncLEDs(control, panel)
		}
	}
}

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
		log.Printf("AVIONICS -> AG1: %v", on)
		ctrl.SetActionGroup(1, on)

	case switchpanel.SwFuel:
		log.Printf("FUEL -> AG2: %v", on)
		ctrl.SetActionGroup(2, on)

	case switchpanel.SwDeice:
		log.Printf("DE-ICE -> AG3: %v", on)
		ctrl.SetActionGroup(3, on)

	case switchpanel.SwPitot:
		log.Printf("PITOT -> AG4: %v", on)
		ctrl.SetActionGroup(4, on)

	case switchpanel.SwCowl:
		log.Printf("COWL -> AG5: %v", on)
		ctrl.SetActionGroup(5, on)

	case switchpanel.SwPanel:
		log.Printf("PANEL -> AG6: %v", on)
		ctrl.SetActionGroup(6, on)

	case switchpanel.SwBeacon:
		log.Printf("BEACON -> AG7: %v", on)
		ctrl.SetActionGroup(7, on)

	case switchpanel.SwNav:
		log.Printf("NAV -> AG8: %v", on)
		ctrl.SetActionGroup(8, on)

	case switchpanel.SwStrobe:
		log.Printf("STROBE -> AG9: %v", on)
		ctrl.SetActionGroup(9, on)

	case switchpanel.SwTaxi:
		log.Printf("TAXI -> AG10: %v", on)
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

// syncLEDs updates the landing gear LEDs to reflect current gear state.
// Green = deployed, Red = retracted.
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
