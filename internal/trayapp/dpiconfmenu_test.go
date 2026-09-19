package trayapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The template the app writes itself and the one tools/install.sh installs are
// the same file as far as a user is concerned, so they must not drift apart.
func TestDefaultDPIConfMatchesTheShippedTemplate(t *testing.T) {
	shipped, err := os.ReadFile(filepath.Join("..", "..", "examples", "dpi.conf"))
	if err != nil {
		t.Fatalf("reading the shipped template: %v", err)
	}
	if string(shipped) != defaultDPIConf {
		t.Errorf("defaultDPIConf and examples/dpi.conf differ (%d vs %d bytes); "+
			"the app and tools/install.sh would write different files",
			len(defaultDPIConf), len(shipped))
	}
}

func TestSeedDPIConfigCreatesTheTemplate(t *testing.T) {
	// A directory level that does not exist yet: on a fresh account neither
	// ~/.config/attackshark-x11 nor ~/.local/state exists.
	path := filepath.Join(t.TempDir(), "attackshark-x11", "dpi.conf")

	if err := seedDPIConfig(path); err != nil {
		t.Fatalf("seedDPIConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the seeded file: %v", err)
	}
	if string(data) != defaultDPIConf {
		t.Errorf("seeded %d bytes, want the %d-byte template", len(data), len(defaultDPIConf))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("seeded file mode = %o, want owner-only access", mode)
	}
}

// An existing file holds the numbers the user set for their own mouse, so it is
// left alone however little is in it.
func TestSeedDPIConfigLeavesAnExistingFileAlone(t *testing.T) {
	path := writeConfig(t, "# nothing set yet\n")

	if err := seedDPIConfig(path); err != nil {
		t.Fatalf("seedDPIConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(data) != "# nothing set yet\n" {
		t.Errorf("existing config was rewritten as %q", data)
	}
}

// The file a fresh installation gets by clicking the menu item must be one the
// app can start with - and it must not carry invented DPI values.
func TestSeededTemplateLoadsWithoutError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dpi.conf")
	if err := seedDPIConfig(path); err != nil {
		t.Fatalf("seedDPIConfig: %v", err)
	}

	stages, ok, err := LoadDPIStages(path)
	if err != nil {
		t.Fatalf("the seeded template is not loadable: %v", err)
	}
	if ok || stages.Configured() {
		t.Errorf("the template reports %q as configured; its values must ship commented out", stages)
	}
}

func TestEditorCommandReadsVisualAndEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := editorCommand(); got != nil {
		t.Errorf("editorCommand() = %v with neither variable set, want nil", got)
	}

	t.Setenv("EDITOR", "  nano -w ")
	if got := editorCommand(); len(got) != 2 || got[0] != "nano" || got[1] != "-w" {
		t.Errorf("editorCommand() = %v, want [nano -w]", got)
	}

	// $VISUAL outranks $EDITOR, the way it does everywhere else.
	t.Setenv("VISUAL", "code -w")
	if got := editorCommand(); len(got) != 2 || got[0] != "code" {
		t.Errorf("editorCommand() = %v, want it to prefer $VISUAL", got)
	}
}

// fakeCommand writes a stand-in for a desktop command that records the path it
// was asked to open, so the menu action can be tested without a session.
func fakeCommand(t *testing.T, dir, name, record string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"" + record + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the %s stand-in: %v", name, err)
	}
}

// recordedPath waits for the stand-in to be run: the editor is started detached,
// so it has not necessarily run yet when openInEditor returns.
func recordedPath(t *testing.T, record string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(record)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatalf("the command was never started (%s: %v)", record, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpenInEditorPrefersXdgOpen(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "opened")
	fakeCommand(t, dir, "xdg-open", record)
	// Only the stand-in is on PATH, so the editor must not be reached.
	t.Setenv("PATH", dir)
	t.Setenv("EDITOR", "sh")

	path := filepath.Join(dir, "dpi.conf")
	if err := openInEditor(path); err != nil {
		t.Fatalf("openInEditor: %v", err)
	}
	if got := recordedPath(t, record); got != path {
		t.Errorf("xdg-open was given %q, want %q", got, path)
	}
}

func TestOpenInEditorFallsBackToTheEditor(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "opened")
	fakeCommand(t, dir, "fake-editor", record)
	t.Setenv("PATH", dir) // no xdg-open anywhere
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", filepath.Join(dir, "fake-editor"))

	path := filepath.Join(dir, "dpi.conf")
	if err := openInEditor(path); err != nil {
		t.Fatalf("openInEditor: %v", err)
	}
	if got := recordedPath(t, record); got != path {
		t.Errorf("the editor was given %q, want %q", got, path)
	}
}

func TestOpenInEditorWithoutAnyEditor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	err := openInEditor(filepath.Join(t.TempDir(), "dpi.conf"))
	if err == nil {
		t.Fatal("openInEditor succeeded with no editor available")
	}
	if !strings.Contains(err.Error(), "xdg-open") {
		t.Errorf("error = %q, want it to name what was missing", err)
	}
}

// The menu action end to end: a click on a fresh installation creates the file
// from the template and asks the desktop to open it.
func TestOpenDPIConfigSeedsTheFileAndOpensIt(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "opened")
	fakeCommand(t, dir, "xdg-open", record)
	t.Setenv("PATH", dir)

	path := filepath.Join(dir, "attackshark-x11", "dpi.conf")
	a := &app{opt: Options{DPIConfigPath: path}}
	a.openDPIConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the menu action did not create %s: %v", path, err)
	}
	if string(data) != defaultDPIConf {
		t.Error("the file created by the menu action is not the template")
	}
	if got := recordedPath(t, record); got != path {
		t.Errorf("xdg-open was given %q, want %q", got, path)
	}
}

// A config path that cannot be created is reported rather than silently doing
// nothing, and the open is still attempted: it may be the stat that failed while
// the file is there. The notifier is nil here, exactly as it is when there is no
// session bus, so this also pins down that the failure path cannot panic on it.
func TestOpenDPIConfigReportsASeedFailure(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "opened")
	fakeCommand(t, dir, "xdg-open", record)
	t.Setenv("PATH", dir)

	// A file where the directory should be, so the path cannot be created.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("writing the blocker: %v", err)
	}
	path := filepath.Join(blocker, "dpi.conf")

	a := &app{opt: Options{DPIConfigPath: path}}
	a.openDPIConfig()

	if _, err := os.Stat(path); err == nil {
		t.Error("the config file was created inside another file")
	}
	if got := recordedPath(t, record); got != path {
		t.Errorf("xdg-open was given %q, want %q", got, path)
	}
}

// An empty path (no home directory, or -dpi-config cleared to empty) means there
// is no file to open, which must not panic or create anything.
func TestOpenDPIConfigWithoutAPath(t *testing.T) {
	a := &app{opt: Options{}}
	a.openDPIConfig()
}
