package main

import (
	"context"
	"encoding/json"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	krpcgo "github.com/atburke/krpc-go"
	"github.com/atburke/krpc-go/spacecenter"

	"ksp-switchpanel/internal/version"
)

//go:embed navball.html
var navballHTML string

const (
	listenAddr  = ":8585"
	pollHz      = 25
	kRPCHost    = "localhost"
	kRPCPort    = "50000"
)

// NavballData is broadcast to all SSE clients each tick.
type NavballData struct {
	Connected  bool       `json:"connected"`
	Pitch      float64    `json:"pitch"`
	Heading    float64    `json:"heading"`
	Roll       float64    `json:"roll"`
	Speed      float64    `json:"speed"`
	Prograde   [3]float64 `json:"prograde"`
	Retrograde [3]float64 `json:"retrograde"`
	Normal     [3]float64 `json:"normal"`
	AntiNormal [3]float64 `json:"antinormal"`
	Radial     [3]float64 `json:"radial"`
	AntiRadial [3]float64 `json:"antiradial"`
}

// hub holds the latest NavballData and a set of SSE listener channels.
type hub struct {
	mu       sync.Mutex
	latest   NavballData
	listeners map[chan NavballData]struct{}
}

func newHub() *hub {
	return &hub{listeners: make(map[chan NavballData]struct{})}
}

func (h *hub) publish(d NavballData) {
	h.mu.Lock()
	h.latest = d
	for ch := range h.listeners {
		select {
		case ch <- d:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *hub) subscribe() chan NavballData {
	ch := make(chan NavballData, 4)
	h.mu.Lock()
	h.listeners[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan NavballData) {
	h.mu.Lock()
	delete(h.listeners, ch)
	h.mu.Unlock()
}

func main() {
	log.Printf("KSP navball server v%s — listening on %s", version.Version, listenAddr)

	h := newHub()
	go pollKRPC(h)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, navballHTML)
	})

	http.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch := h.subscribe()
		defer h.unsubscribe(ch)

		// Send current state immediately.
		h.mu.Lock()
		cur := h.latest
		h.mu.Unlock()
		sendSSE(w, flusher, cur)

		for {
			select {
			case <-r.Context().Done():
				return
			case d := <-ch:
				sendSSE(w, flusher, d)
			}
		}
	})

	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func sendSSE(w http.ResponseWriter, f http.Flusher, d NavballData) {
	b, _ := json.Marshal(d)
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

func pollKRPC(h *hub) {
	tick := time.NewTicker(time.Second / pollHz)
	defer tick.Stop()

	for {
		connectAndStream(h, tick)
		// publish disconnected state so clients know
		h.publish(NavballData{Connected: false})
		// brief pause before retry
		time.Sleep(2 * time.Second)
	}
}

func connectAndStream(h *hub, tick *time.Ticker) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := krpcgo.KRPCClientConfig{
		Host:       kRPCHost,
		RPCPort:    kRPCPort,
		ClientName: "navball",
		RPCOnly:    true,
	}
	client := krpcgo.NewKRPCClient(cfg)
	if err := client.Connect(ctx); err != nil {
		log.Printf("kRPC connect failed: %v", err)
		return
	}
	defer client.Close()
	log.Println("kRPC connected")

	sc := spacecenter.New(client)

	for {
		<-tick.C

		vessel, err := sc.ActiveVessel()
		if err != nil {
			log.Printf("No active vessel: %v", err)
			h.publish(NavballData{Connected: false})
			continue
		}

		orbit, err := vessel.Orbit()
		if err != nil {
			continue
		}
		body, err := orbit.Body()
		if err != nil {
			continue
		}
		// Surface reference frame: x=east, y=up, z=north — same axes as Three.js world.
		surfRef, err := body.ReferenceFrame()
		if err != nil {
			continue
		}

		flight, err := vessel.Flight(surfRef)
		if err != nil {
			continue
		}

		pitch, err := flight.Pitch()
		if err != nil {
			continue
		}
		heading, err := flight.Heading()
		if err != nil {
			continue
		}
		roll, err := flight.Roll()
		if err != nil {
			continue
		}
		speed, err := flight.Speed()
		if err != nil {
			continue
		}
		pg, err := flight.Prograde()
		if err != nil {
			continue
		}
		rg, err := flight.Retrograde()
		if err != nil {
			continue
		}
		nm, err := flight.Normal()
		if err != nil {
			continue
		}
		an, err := flight.AntiNormal()
		if err != nil {
			continue
		}
		rd, err := flight.Radial()
		if err != nil {
			continue
		}
		ar, err := flight.AntiRadial()
		if err != nil {
			continue
		}

		h.publish(NavballData{
			Connected:  true,
			Pitch:      float64(pitch),
			Heading:    float64(heading),
			Roll:       float64(roll),
			Speed:      float64(speed),
			Prograde:   [3]float64{pg.A, pg.B, pg.C},
			Retrograde: [3]float64{rg.A, rg.B, rg.C},
			Normal:     [3]float64{nm.A, nm.B, nm.C},
			AntiNormal: [3]float64{an.A, an.B, an.C},
			Radial:     [3]float64{rd.A, rd.B, rd.C},
			AntiRadial: [3]float64{ar.A, ar.B, ar.C},
		})
	}
}
