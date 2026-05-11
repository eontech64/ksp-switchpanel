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
	"ksp-switchpanel/internal/version"
)

const appVersion = version.Version

func main() {
	log.Printf("KSP panels bridge (UI) v%s starting...", appVersion)

	cfg, err := config.FindAndLoad()
	if err != nil {
		log.Fatalf("Config: %v", err)
	}

	statusCh := make(chan bridge.BridgeStatus, 8)
	quitCh := make(chan struct{})

	go bridge.RunBridge(statusCh, quitCh, cfg)

	runUI(statusCh, quitCh)
}

func runUI(statusCh <-chan bridge.BridgeStatus, quitCh chan struct{}) {
	a := app.New()
	w := a.NewWindow("KSP Panel Bridge  v" + appVersion)
	w.Resize(fyne.NewSize(320, 410))
	w.SetFixedSize(true)

	// Bindings
	swVal := binding.NewString()
	rdVal := binding.NewString()
	muVal := binding.NewString()
	krpcVal := binding.NewString()
	vesselVal := binding.NewString()
	mjVal := binding.NewString()
	apSpeedVal := binding.NewString()
	apAltVal := binding.NewString()

	// Initial state — panels are detected dynamically via status updates.
	swVal.Set("○ Not found")
	rdVal.Set("○ Not found")
	muVal.Set("○ Not found")
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
			if s.MultiPanel {
				muVal.Set("● Connected")
			} else {
				muVal.Set("○ Not found")
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
		"KSP Panel Bridge  v"+appVersion,
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	hwForm := widget.NewForm(
		widget.NewFormItem("Switch Panel", widget.NewLabelWithData(swVal)),
		widget.NewFormItem("Radio Panel", widget.NewLabelWithData(rdVal)),
		widget.NewFormItem("Multi Panel", widget.NewLabelWithData(muVal)),
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
