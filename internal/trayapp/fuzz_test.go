package trayapp

import (
	"os"
	"path/filepath"
	"testing"

	"attackshark-x11/internal/mouse"
)

// The config loader is fed files the user edits by hand, so it is fuzzed for the
// two failures that matter: accepting a file it should reject, and rejecting one
// it should accept - the second being worse, because the tray refuses to start.
func FuzzLoadDPIStages(f *testing.F) {
	f.Add(goodConfig)
	f.Add("# nothing set yet\n")
	f.Add("1=800\n2=1600\n3=2400\n4=3200\n5=6400\n")
	f.Add("6=22000\n1=800\n3=2400\n2=1600\n5=6400\n4=3200\n")
	f.Add("1 = 800\n 2=1600\n# midway\n3=2400\n4=3200\n5=6400\n6=22000")
	f.Add("1=0\n2=1600\n3=2400\n4=3200\n5=6400\n6=22000\n")
	f.Add("1=800\n2=1600\n3=2400\n4=3200\n5=6400\n6=22001\n")
	f.Add("1=800\n2=1600\n3=2400\n")
	f.Add("3=2400\n")
	f.Add("")
	f.Add("\x00\x01\x02")

	// One file reused by every iteration: the fuzzer runs a target's
	// iterations in sequence, and a temp directory per case is a lot of churn.
	path := filepath.Join(f.TempDir(), "dpi.conf")

	f.Fuzz(func(t *testing.T, content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		stages, ok, err := LoadDPIStages(path)
		if err != nil {
			// A rejected file must not be half applied: the app is expected
			// to refuse to start rather than use a partial mapping.
			if ok || stages.Configured() {
				t.Fatalf("LoadDPIStages returned %q with error %v", stages, err)
			}
			return
		}
		if !ok {
			if stages.Configured() {
				t.Fatalf("LoadDPIStages reported ok=false with %q", stages)
			}
			return
		}

		// Everything that loaded has to be a complete mapping inside the range
		// the sensor supports - that is what makes the value safe to show.
		if !stages.Configured() {
			t.Fatal("LoadDPIStages reported ok for nothing configured")
		}
		for stage := 1; stage <= mouse.MaxStage; stage++ {
			value, found := stages.At(stage)
			if !found {
				t.Fatalf("stage %d is missing from a file that loaded as configured", stage)
			}
			if value < 1 || value > mouse.MaxDPI {
				t.Fatalf("stage %d = %d, outside 1-%d", stage, value, mouse.MaxDPI)
			}
		}
	})
}
