// hidtest verifies that the Saitek Switch Panel can be opened via hidapi/IOKit on macOS.
package main

import (
	"fmt"
	"log"

	hid "github.com/sstallion/go-hid"
)

const (
	vendorID  = 0x06a3
	productID = 0x0d67
)

func main() {
	if err := hid.Init(); err != nil {
		log.Fatalf("hid.Init: %v", err)
	}
	defer hid.Exit()

	fmt.Println("Opening Saitek Switch Panel (VID=06a3, PID=0d67) via hidapi/IOKit...")
	dev, err := hid.OpenFirst(vendorID, productID)
	if err != nil {
		log.Fatalf("Failed to open device: %v", err)
	}
	defer dev.Close()
	fmt.Println("Device opened successfully!")

	info, err := dev.GetDeviceInfo()
	if err == nil {
		fmt.Printf("  Manufacturer: %s\n", info.MfrStr)
		fmt.Printf("  Product:      %s\n", info.ProductStr)
	}

	fmt.Println("\nReading switch states (move a switch to see output, Ctrl+C to stop)...")
	buf := make([]byte, 4) // 1 report ID + 3 data bytes
	for {
		n, err := dev.Read(buf)
		if err != nil {
			log.Fatalf("Read error: %v", err)
		}
		if n > 0 {
			fmt.Printf("Raw bytes (%d): %08b %08b %08b\n", n, buf[0], buf[1], buf[2])
		}
	}
}
