// Standalone test to explore the radio panel's HID protocol.
// Run: go run hidtest/radiopanel_test.go
package main

import (
	"fmt"
	"log"
	"time"

	hid "github.com/sstallion/go-hid"
)

const (
	radioVendorID  = 0x06a3
	radioProductID = 0x0d05
)

func main() {
	hid.Init()
	defer hid.Exit()

	fmt.Println("Opening Logitech Flight Radio Panel...")
	dev, err := hid.OpenFirst(radioVendorID, radioProductID)
	if err != nil {
		log.Fatalf("Failed to open: %v", err)
	}
	defer dev.Close()
	fmt.Println("Opened OK")

	// The radio panel display is controlled by a 23-byte feature report:
	// byte 0: report ID (0x00)
	// bytes 1-5:   top-left display (5 digits)
	// bytes 6-10:  top-right display (5 digits)
	// bytes 11-15: bottom-left display (5 digits)
	// bytes 16-20: bottom-right display (5 digits)
	// bytes 21-22: padding (0x00)
	//
	// Each digit:
	//   0x00-0x09 = digits 0-9
	//   0x0f      = blank
	//   0xd0-0xd9 = digit with dot
	//   0xe0-0xe9 = dash

	// Test: show "12345" on top-left, "67890" on top-right,
	//             "  ALT" (dashes) on bottom-left, "  SPD" (dashes) on bottom-right
	report := make([]byte, 23)
	report[0] = 0x00 // report ID

	// Top-left: 1 2 3 4 5
	report[1] = 0x01
	report[2] = 0x02
	report[3] = 0x03
	report[4] = 0x04
	report[5] = 0x05

	// Top-right: 6 7 8 9 0
	report[6] = 0x06
	report[7] = 0x07
	report[8] = 0x08
	report[9] = 0x09
	report[10] = 0x00

	// Bottom-left: all dashes
	report[11] = 0xe0
	report[12] = 0xe0
	report[13] = 0xe0
	report[14] = 0xe0
	report[15] = 0xe0

	// Bottom-right: all blank
	report[16] = 0x0f
	report[17] = 0x0f
	report[18] = 0x0f
	report[19] = 0x0f
	report[20] = 0x0f

	report[21] = 0x00
	report[22] = 0x00

	fmt.Println("Sending test display data...")
	if _, err := dev.SendFeatureReport(report); err != nil {
		log.Printf("SendFeatureReport failed: %v", err)
	} else {
		fmt.Println("Display updated. Check the panel!")
	}

	time.Sleep(3 * time.Second)

	// Clear display
	for i := 1; i <= 20; i++ {
		report[i] = 0x0f
	}
	dev.SendFeatureReport(report)
	fmt.Println("Display cleared.")

	// Read switches for 5 seconds
	fmt.Println("\nReading switches (move a knob/switch)...")
	dev.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			break
		}
		if n > 0 {
			fmt.Printf("  bytes[%d]: %08b %08b %08b\n", n, buf[0], buf[1], buf[2])
		}
	}
}
