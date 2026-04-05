// Package config loads and provides the panel bridge configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration structure.
type Config struct {
	Switches map[string]string   `toml:"switches"`
	Radio    map[string][]string `toml:"radio"`
}

// DisplaySpec is the parsed form of a "field:decimals" radio display entry.
type DisplaySpec struct {
	Field    string
	Decimals int
}

// ParseDisplaySpec parses "field:decimals" (decimals optional, defaults to 0).
func ParseDisplaySpec(s string) (DisplaySpec, error) {
	parts := strings.SplitN(s, ":", 2)
	spec := DisplaySpec{Field: parts[0]}
	if len(parts) == 2 {
		d, err := strconv.Atoi(parts[1])
		if err != nil {
			return spec, fmt.Errorf("invalid decimals in %q: %w", s, err)
		}
		spec.Decimals = d
	}
	return spec, nil
}

// Default returns the built-in default configuration, matching the original hardcoded behaviour.
func Default() *Config {
	return &Config{
		Switches: map[string]string{
			"BAT":      "rcs",
			"ALT":      "sas",
			"AVIONICS": "action_group:1",
			"FUEL":     "action_group:2",
			"DEICE":    "action_group:3",
			"PITOT":    "action_group:4",
			"COWL":     "action_group:5",
			"PANEL":    "action_group:6",
			"BEACON":   "action_group:7",
			"NAV":      "action_group:8",
			"STROBE":   "action_group:9",
			"TAXI":     "action_group:10",
			"LANDING":  "brakes",
			"GEAR_DOWN": "gear_down",
			"GEAR_UP":   "gear_up",
			"ROT_OFF":   "sas_mode:stability_assist",
			"ROT_R":     "sas_mode:prograde",
			"ROT_L":     "sas_mode:retrograde",
			"ROT_BOTH":  "sas_mode:target",
			"ROT_START": "sas_mode:anti_target",
		},
		Radio: map[string][]string{
			"COM1": {"altitude_km:1", "vspeed:0"},
			"COM2": {"apoapsis_km:0", "periapsis_km:0"},
			"NAV1": {"speed_kts:0", "heading:0"},
			"NAV2": {"pitch:1", "roll:1"},
			"ADF":  {"latitude:2", "longitude:2"},
			"DME":  {"gforce:2", "dynpressure:0"},
			"XPDR": {"time_to_apo:0", "time_to_peri:0"},
		},
	}
}

// Load reads a TOML config file, merging its values over the built-in defaults.
// Returns the default config if the file does not exist.
func Load(path string) (*Config, error) {
	cfg := Default()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("load config %q: %w", path, err)
	}
	return cfg, nil
}

// FindAndLoad looks for panels.toml next to the binary, then in the current
// working directory. Falls back to built-in defaults if not found.
func FindAndLoad() (*Config, error) {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "panels.toml")
		if _, err := os.Stat(p); err == nil {
			return Load(p)
		}
	}
	if _, err := os.Stat("panels.toml"); err == nil {
		return Load("panels.toml")
	}
	return Default(), nil
}
