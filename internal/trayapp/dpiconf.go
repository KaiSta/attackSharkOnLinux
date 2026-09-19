package trayapp

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"attackshark-x11/internal/mouse"
)

// DefaultDPIConfigPath is where the per-stage DPI values are read from:
//
//	$XDG_CONFIG_HOME/attackshark-x11/dpi.conf
//
// or ~/.config/attackshark-x11/dpi.conf when XDG_CONFIG_HOME is unset.
func DefaultDPIConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "attackshark-x11", "dpi.conf")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "attackshark-x11", "dpi.conf")
}

// LoadDPIStages reads a "stage=value" config file. Blank lines and lines
// starting with # are ignored; every stage from 1 to 6 must appear exactly once:
//
//	# DPI value of each stage, as shown by the vendor's software.
//	1=800
//	2=1600
//	3=2400
//	4=3200
//	5=6400
//	6=22000
//
// A missing file is not an error: ok is false and the app simply shows the
// stage without a DPI value. A malformed file is an error, because silently
// discarding values the user deliberately wrote would be worse than refusing to
// start.
func LoadDPIStages(path string) (mouse.DPIStages, bool, error) {
	var none mouse.DPIStages
	if path == "" {
		return none, false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return none, false, nil
		}
		return none, false, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()

	values := make(map[int]int, mouse.MaxStage)
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, found := strings.Cut(text, "=")
		if !found {
			return none, false, fmt.Errorf("%s line %d: expected \"stage=value\", got %q", path, line, text)
		}
		stage, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			return none, false, fmt.Errorf("%s line %d: %q is not a stage number", path, line, key)
		}
		if stage < 1 || stage > mouse.MaxStage {
			return none, false, fmt.Errorf("%s line %d: stage %d is outside 1-%d", path, line, stage, mouse.MaxStage)
		}
		if _, dup := values[stage]; dup {
			return none, false, fmt.Errorf("%s line %d: stage %d is set twice", path, line, stage)
		}
		dpi, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return none, false, fmt.Errorf("%s line %d: %q is not a DPI value", path, line, value)
		}
		values[stage] = dpi
	}
	if err := scanner.Err(); err != nil {
		return none, false, fmt.Errorf("reading %s: %w", path, err)
	}

	if len(values) == 0 {
		return none, false, nil
	}
	for stage := 1; stage <= mouse.MaxStage; stage++ {
		if _, ok := values[stage]; !ok {
			return none, false, fmt.Errorf("%s: stage %d is missing (all %d stages are required)",
				path, stage, mouse.MaxStage)
		}
	}

	// Reuse the shared parser so the file and the command line enforce exactly
	// the same bounds.
	ordered := make([]string, mouse.MaxStage)
	for stage := 1; stage <= mouse.MaxStage; stage++ {
		ordered[stage-1] = strconv.Itoa(values[stage])
	}
	stages, err := mouse.ParseDPIStages(strings.Join(ordered, ","))
	if err != nil {
		return none, false, fmt.Errorf("%s: %w", path, err)
	}
	return stages, true, nil
}
