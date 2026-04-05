package main

import (
	"fmt"
	"log"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"

	"ksp-switchpanel/config"
	"ksp-switchpanel/internal/bridge"
	"ksp-switchpanel/radiopanel"
	"ksp-switchpanel/switchpanel"
)

const version = "1.0.0"

func main() {
	log.Printf("KSP panels bridge (UI) v%s starting...", version)

	cfg, err := config.FindAndLoad()
	if err != nil {
		log.Fatalf("Config: %v", err)
	}

	log.Println("Connecting to Switch Panel...")
	swPanel, err := switchpanel.Open()
	if err != nil {
		log.Printf("Switch panel not found: %v", err)
		swPanel = nil
	} else {
		log.Println("Switch panel connected.")
		defer swPanel.Close()
	}

	log.Println("Connecting to Radio Panel...")
	radioPanel, err := radiopanel.Open()
	if err != nil {
		log.Printf("Radio panel not found: %v", err)
		radioPanel = nil
	} else {
		log.Println("Radio panel connected.")
		defer radioPanel.Close()
	}

	if swPanel == nil && radioPanel == nil {
		log.Fatal("No panels found. Connect at least one panel and try again.")
	}

	statusCh := make(chan bridge.BridgeStatus, 8)
	quitCh := make(chan struct{})

	go bridge.RunBridge(statusCh, quitCh, cfg, swPanel, radioPanel)

	runUI(statusCh, quitCh, swPanel != nil, radioPanel != nil)
}

func runUI(statusCh <-chan bridge.BridgeStatus, quitCh chan struct{}, hasSW, hasRD bool) {
	a := app.New()
	w := a.NewWindow("KSP Panel Bridge  v" + version)
	w.Resize(fyne.NewSize(320, 380))
	w.SetFixedSize(true)

	// Bindings
	swVal := binding.NewString()
	rdVal := binding.NewString()
	krpcVal := binding.NewString()
	vesselVal := binding.NewString()
	mjVal := binding.NewString()
	apSpeedVal := binding.NewString()
	apAltVal := binding.NewString()

	// Initial state based on hardware detection
	if hasSW {
		swVal.Set("● Connected")
	} else {
		swVal.Set("○ Not found")
	}
	if hasRD {
		rdVal.Set("● Connected")
	} else {
		rdVal.Set("○ Not found")
	}
	krpcVal.Set("↻ Connecting...")
	vesselVal.Set("—")
	mjVal.Set("○ N/A")
	apSpeedVal.Set("—")
	apAltVal.Set("—")

	// Status update goroutine
	go func() {
		for s := range statusCh {
			if s.SwitchPanel {
				swVal.Set("● Connected")
			} else {
				swVal.Set("○ Not found")
			}
			if s.RadioPanel {
				rdVal.Set("● Connected")
			} else {
				rdVal.Set("○ Not found")
			}
			switch s.KRPC {
			case "connected":
				krpcVal.Set("● Connected")
			default:
				krpcVal.Set("↻ Connecting...")
			}
			if s.VesselName != "" {
				vesselVal.Set(s.VesselName)
			} else {
				vesselVal.Set("—")
			}
			if s.MechJeb {
				mjVal.Set("● Ready")
				if s.APSpeedHold {
					apSpeedVal.Set(fmt.Sprintf("● ON  %.0f m/s", s.APSpeedTarget))
				} else {
					apSpeedVal.Set(fmt.Sprintf("○ OFF  %.0f m/s", s.APSpeedTarget))
				}
				if s.APAltHold {
					apAltVal.Set(fmt.Sprintf("● ON  %.0f m", s.APAltTarget))
				} else {
					apAltVal.Set(fmt.Sprintf("○ OFF  %.0f m", s.APAltTarget))
				}
			} else {
				mjVal.Set("○ N/A")
				apSpeedVal.Set("—")
				apAltVal.Set("—")
			}
		}
	}()

	// Quit logic: safe to call multiple times
	var once sync.Once
	doQuit := func() {
		once.Do(func() {
			close(quitCh)
		})
	}

	// UI layout
	title := widget.NewLabelWithStyle(
		"KSP Panel Bridge  v"+version,
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	hwForm := widget.NewForm(
		widget.NewFormItem("Switch Panel", widget.NewLabelWithData(swVal)),
		widget.NewFormItem("Radio Panel", widget.NewLabelWithData(rdVal)),
	)
	krpcForm := widget.NewForm(
		widget.NewFormItem("kRPC", widget.NewLabelWithData(krpcVal)),
		widget.NewFormItem("Active Vessel", widget.NewLabelWithData(vesselVal)),
		widget.NewFormItem("MechJeb", widget.NewLabelWithData(mjVal)),
	)
	apForm := widget.NewForm(
		widget.NewFormItem("Speed Hold", widget.NewLabelWithData(apSpeedVal)),
		widget.NewFormItem("Alt Hold", widget.NewLabelWithData(apAltVal)),
	)

	stopBtn := widget.NewButton("Stop", func() {
		doQuit()
		w.Close()
	})

	content := container.NewVBox(
		title,
		widget.NewSeparator(),
		hwForm,
		widget.NewSeparator(),
		krpcForm,
		widget.NewSeparator(),
		apForm,
		widget.NewSeparator(),
		container.NewCenter(stopBtn),
	)

	w.SetContent(content)
	w.SetOnClosed(func() {
		doQuit()
	})
	w.ShowAndRun()
}
