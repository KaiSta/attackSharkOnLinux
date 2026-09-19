package trayapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"attackshark-x11/internal/mouse"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dpi.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

const goodConfig = `# DPI value of each stage, as shown by the vendor's software.
1=800
2=1600
3=2400
4=3200
5=6400
6=22000
`

func TestLoadDPIStages(t *testing.T) {
	path := writeConfig(t, goodConfig)
	stages, ok, err := LoadDPIStages(path)
	if err != nil {
		t.Fatalf("LoadDPIStages: %v", err)
	}
	if !ok {
		t.Fatal("LoadDPIStages reported nothing configured for a complete file")
	}
	if got, want := stages.String(), "800/1600/2400/3200/6400/22000"; got != want {
		t.Errorf("loaded %q, want %q", got, want)
	}
}

func TestLoadDPIStagesMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.conf")
	stages, ok, err := LoadDPIStages(path)
	if err != nil {
		t.Fatalf("a missing file should not be an error, got %v", err)
	}
	if ok || stages.Configured() {
		t.Fatal("a missing file reported configured values")
	}
}

func TestLoadDPIStagesTolerance(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "comments, blanks and padding",
			content: "\n# only comments here\n\n1 = 800\n 2=1600\n# midway\n3=2400\n4=3200\n5=6400\n6=22000\n\n",
			want:    "800/1600/2400/3200/6400/22000",
		},
		{
			name:    "unordered lines",
			content: "6=22000\n1=800\n3=2400\n2=1600\n5=6400\n4=3200\n",
			want:    "800/1600/2400/3200/6400/22000",
		},
		{
			name:    "no trailing newline",
			content: "1=800\n2=1600\n3=2400\n4=3200\n5=6400\n6=22000",
			want:    "800/1600/2400/3200/6400/22000",
		},
		{
			name:    "comments only means unconfigured",
			content: "# nothing set yet\n",
			want:    "",
		},
		{
			name:    "empty file means unconfigured",
			content: "",
			want:    "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stages, ok, err := LoadDPIStages(writeConfig(t, tc.content))
			if err != nil {
				t.Fatalf("LoadDPIStages: %v", err)
			}
			if tc.want == "" {
				if ok || stages.Configured() {
					t.Fatalf("reported configured %q, want nothing configured", stages)
				}
				return
			}
			if !ok {
				t.Fatal("nothing configured, want values")
			}
			if got := stages.String(); got != tc.want {
				t.Errorf("loaded %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadDPIStagesRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "missing stage", content: "1=800\n2=1600\n3=2400\n", wantErr: "stage 4 is missing"},
		{name: "duplicate stage", content: goodConfig + "3=2400\n", wantErr: "stage 3 is set twice"},
		{name: "no equals sign", content: "800\n", wantErr: `expected "stage=value"`},
		{name: "stage zero", content: "0=800\n", wantErr: "outside 1-6"},
		{name: "stage seven", content: "7=800\n", wantErr: "outside 1-6"},
		{name: "non numeric stage", content: "one=800\n", wantErr: "is not a stage number"},
		{name: "non numeric value", content: "1=eight\n", wantErr: "is not a DPI value"},
		{name: "value above sensor maximum", content: "1=800\n2=1600\n3=2400\n4=3200\n5=6400\n6=44000\n", wantErr: "outside 1-22000"},
		{name: "value of zero", content: "1=0\n2=1600\n3=2400\n4=3200\n5=6400\n6=22000\n", wantErr: "outside 1-22000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := LoadDPIStages(writeConfig(t, tc.content))
			if err == nil {
				t.Fatalf("LoadDPIStages accepted a bad file, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestDefaultDPIConfigPathUsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	want := filepath.Join("/tmp/xdg-test", "attackshark-x11", "dpi.conf")
	if got := DefaultDPIConfigPath(); got != want {
		t.Errorf("DefaultDPIConfigPath() = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got := DefaultDPIConfigPath()
	if got != "" && !strings.HasSuffix(got, filepath.Join(".config", "attackshark-x11", "dpi.conf")) {
		t.Errorf("DefaultDPIConfigPath() = %q, want it to end in .config/attackshark-x11/dpi.conf", got)
	}
}

// TestDPIValuesSurviveTheRoundTrip guards the contract the tray relies on: what
// the file says is what the menu shows.
func TestDPIValuesSurviveTheRoundTrip(t *testing.T) {
	stages, ok, err := LoadDPIStages(writeConfig(t, goodConfig))
	if err != nil || !ok {
		t.Fatalf("loading: %v (ok=%v)", err, ok)
	}
	for stage := 1; stage <= mouse.MaxStage; stage++ {
		if _, found := stages.At(stage); !found {
			t.Errorf("stage %d missing after loading", stage)
		}
	}
	if desc := stages.StageDescription(3); desc != "2400 DPI" {
		t.Errorf("StageDescription(3) = %q, want %q", desc, "2400 DPI")
	}
}
