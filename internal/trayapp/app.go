// Package trayapp is the Attack Shark X11 tray application.
//
// One goroutine owns the device file descriptor and reads the receiver's status
// channel; it drives the tray icon, tooltip and menu from what arrives. The app
// is strictly read-only: it never writes to the mouse, which is why the DPI
// stage is reported but cannot be changed from here.
//
// The mouse announces its stage number when it changes, so the stage itself is
// telemetry. The DPI value behind each stage is not: the device does not report
// it and the vendor publishes only the stage count and the sensor's maximum, so
// the values are read from a small config file instead. See README.md.
//
// Every reading is timestamped, so a number that has stopped arriving is shown
// as "last read 12 min ago" rather than being passed off as current. The mouse
// pushes a heartbeat every two seconds while it is awake, which is what makes
// silence worth reporting at all.
package trayapp

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"

	"attackshark-x11/internal/batteryicon"
	"attackshark-x11/internal/hidraw"
	"attackshark-x11/internal/mouse"
	"attackshark-x11/internal/notify"
)

// Options configures the application.
type Options struct {
	// IconSize is the tray icon edge length in pixels. The panel scales it, so
	// this only needs to be large enough for a crisp glyph.
	IconSize int
	// DPI is the DPI value of each stage, used to label the stage the mouse
	// reports. The zero value means "not configured", in which case
	// DPIConfigPath is read instead.
	DPI mouse.DPIStages
	// DPIConfigPath is the file to read DPI values from when DPI is not set on
	// the command line. Loaded only if it exists.
	DPIConfigPath string
	// PollTimeout bounds each device read. It is also how often the loop wakes
	// up to notice a vanished device.
	PollTimeout time.Duration
	// RetryEvery is how long to wait between attempts to open the device.
	RetryEvery time.Duration
	// StateDir remembers the last observed DPI stage between runs. An empty
	// string disables persistence.
	StateDir string
	// NoNotify disables desktop notifications.
	NoNotify bool
	// Verbose logs device activity to stderr.
	Verbose bool
}

// DefaultOptions returns the options the tray command starts from.
func DefaultOptions() Options {
	return Options{
		IconSize:      24,
		DPIConfigPath: DefaultDPIConfigPath(),
		PollTimeout:   time.Second,
		RetryEvery:    3 * time.Second,
		StateDir:      defaultStateDir(),
	}
}

func defaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "attackshark-x11")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "attackshark-x11")
}

// How fresh a battery reading is treated as. The mouse pushes one every ~2 s
// while it is awake.
const (
	// heartbeat is the observed interval between battery reports.
	heartbeat = 2 * time.Second
	// freshFor is how long a reading still counts as current. Four missed
	// heartbeats is the first point worth mentioning to anyone.
	freshFor = 4 * heartbeat
	// quietAfter is when silence stops looking like a missed heartbeat and
	// starts looking like a mouse that is switched off, asleep or out of range.
	quietAfter = time.Minute
)

// alerts tracks which battery warnings have already been shown, so a level that
// hovers at a threshold does not notify on every heartbeat.
type alerts struct {
	low20 bool
	low10 bool
	full  bool
}

type app struct {
	opt      Options
	notifier *notify.Notifier

	mu sync.Mutex
	// device and reported state
	dev            *hidraw.Device
	devicePath     string
	lastErr        error
	connected      bool
	battery        mouse.Battery
	haveBattery    bool
	stage          mouse.Stage
	haveStage      bool
	stageConfirmed bool
	alerts         alerts
	iconKey        string
	// batterySeen is when the last battery report arrived. The zero value means
	// "no timestamp", which is reported as a current reading rather than
	// invented as an age.
	batterySeen time.Time
	// silenceLogged makes the "no reports" line appear once per quiet spell
	// rather than once per poll.
	silenceLogged bool

	// Last text handed to the panel, so the per-second freshness update does not
	// churn D-Bus properties when nothing visible has changed.
	lastTooltip     string
	lastBatteryText string
	lastStatusText  string

	done chan struct{}

	// menu items
	itemBattery *systray.MenuItem
	itemStage   *systray.MenuItem
	itemStatus  *systray.MenuItem
	itemDPI     *systray.MenuItem
	stageItems  []*systray.MenuItem
}

// Run starts the tray application and blocks until it exits.
func Run(opt Options) error {
	if opt.IconSize <= 0 {
		return fmt.Errorf("icon size must be positive, got %d", opt.IconSize)
	}
	if opt.PollTimeout <= 0 {
		return errors.New("poll timeout must be positive")
	}
	// Before anything is opened: one tray app per session, so that a second
	// launcher (an autostart entry and a systemd unit both enabled, say) exits
	// with a reason instead of fighting the first over the receiver's reports.
	release, err := acquireSingleInstanceLock()
	if err != nil {
		return err
	}
	defer release()

	if opt.StateDir != "" {
		if err := os.MkdirAll(opt.StateDir, 0o700); err != nil {
			// Not fatal: the app just cannot remember the stage between runs.
			opt.StateDir = ""
		}
	}
	if !opt.DPI.Configured() && opt.DPIConfigPath != "" {
		stages, ok, err := LoadDPIStages(opt.DPIConfigPath)
		if err != nil {
			return err
		}
		if ok {
			opt.DPI = stages
		}
	}

	a := &app{opt: opt, done: make(chan struct{})}
	if !opt.NoNotify {
		if notifier, err := notify.New(); err == nil {
			a.notifier = notifier
		} else {
			a.log("notifications disabled: %v", err)
		}
	}
	a.loadStage()

	go a.watchSignals()
	systray.Run(a.onReady, a.onExit)
	return nil
}

func (a *app) watchSignals() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	a.log("signal received, shutting down")
	systray.Quit()
}

func (a *app) log(format string, args ...any) {
	if a.opt.Verbose {
		fmt.Fprintf(os.Stderr, "[attackshark] %s\n", fmt.Sprintf(format, args...))
	}
}

func (a *app) onReady() {
	systray.SetTitle("Attack Shark X11")
	systray.SetTooltip("Attack Shark X11 - starting up")

	a.itemBattery = systray.AddMenuItem("Battery: waiting", "Battery level reported by the mouse")
	a.itemBattery.Disable()
	a.itemStage = systray.AddMenuItem("DPI: waiting", "DPI stage reported by the mouse")
	a.itemStage.Disable()

	// Read-only list of the six stages and the DPI value configured for each.
	// It exists to show what the reported stage number means; there is no way to
	// change the stage from here, and no menu item pretends otherwise.
	a.itemDPI = systray.AddMenuItem("DPI stages", a.dpiMenuTooltip())
	for i := 1; i <= mouse.MaxStage; i++ {
		title := fmt.Sprintf("Stage %d", i)
		if desc := a.opt.DPI.StageDescription(i); desc != "" {
			title = fmt.Sprintf("Stage %d - %s", i, desc)
		}
		item := a.itemDPI.AddSubMenuItemCheckbox(title, a.stageItemTooltip(i), false)
		// The stage follows the mouse's own DPI button, so these are labels
		// rather than controls.
		item.Disable()
		a.stageItems = append(a.stageItems, item)
	}

	systray.AddSeparator()
	a.itemStatus = systray.AddMenuItem("starting up", "Connection state")
	a.itemStatus.Disable()

	reconnect := systray.AddMenuItem("Reconnect now", "Try to open the receiver again")
	forgetStage := systray.AddMenuItem("Forget known DPI stage", "Show the stage as unknown until the mouse reports one again")

	// The DPI values are the one thing the device cannot supply, so the file
	// holding them is worth reaching without leaving the menu. It is created
	// from the annotated template if it is not there yet.
	openConf := systray.AddMenuItem("Open DPI config", a.openConfigTooltip())
	if a.opt.DPIConfigPath == "" {
		openConf.Disable()
	}

	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Stop the tray application")

	go a.deviceLoop()
	go a.eventLoop(reconnect, forgetStage, openConf, quit)

	a.refresh()
}

// openConfigTooltip says both what the menu item opens and when the file's
// values take effect, because the second half is not obvious.
func (a *app) openConfigTooltip() string {
	if a.opt.DPIConfigPath == "" {
		return "No DPI config path is configured, so there is no file to open"
	}
	return fmt.Sprintf("Open %s in your editor. The tray reads it at startup, so restart to apply changes.",
		a.opt.DPIConfigPath)
}

// dpiMenuTooltip explains where the listed DPI values come from, since the
// device cannot supply them.
func (a *app) dpiMenuTooltip() string {
	base := fmt.Sprintf("The mouse's %d DPI stages, and the DPI value configured for each. "+
		"The value comes from your settings, not from the device.", mouse.MaxStage)
	if !a.opt.DPI.Configured() {
		base += fmt.Sprintf(" Nothing is configured yet; the %s sensor supports up to %d DPI.",
			mouse.SensorName, mouse.MaxDPI)
	}
	return base
}

func (a *app) stageItemTooltip(stage int) string {
	if desc := a.opt.DPI.StageDescription(stage); desc != "" {
		return fmt.Sprintf("Stage %d is set to %s. Change it with the mouse's DPI button.", stage, desc)
	}
	return fmt.Sprintf("Stage %d. Its DPI value is not configured - see the README for where to find it, then set it in %s.",
		stage, a.opt.DPIConfigPath)
}

func (a *app) onExit() {
	select {
	case <-a.done:
	default:
		close(a.done)
	}

	a.mu.Lock()
	dev := a.dev
	a.dev = nil
	a.mu.Unlock()
	if dev != nil {
		dev.Close()
	}
}

func (a *app) eventLoop(reconnect, forgetStage, openConf, quit *systray.MenuItem) {
	for {
		select {
		case <-a.done:
			return
		case <-reconnect.ClickedCh:
			a.mu.Lock()
			dev := a.dev
			a.dev = nil
			a.connected = false
			a.mu.Unlock()
			if dev != nil {
				// Closing makes the blocked read fail, so deviceLoop reopens.
				dev.Close()
			}
			a.log("reconnect requested")
			a.refresh()
		case <-forgetStage.ClickedCh:
			a.mu.Lock()
			a.haveStage = false
			a.stageConfirmed = false
			a.mu.Unlock()
			a.clearSavedStage()
			a.refresh()
		case <-openConf.ClickedCh:
			a.openDPIConfig()
		case <-quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// deviceLoop keeps a device open and feeds its reports to the UI, reopening it
// whenever it disappears.
func (a *app) deviceLoop() {
	buf := make([]byte, hidraw.ReadBufferSize)

	for {
		select {
		case <-a.done:
			return
		default:
		}

		dev := a.currentDevice()
		if dev == nil {
			opened, err := hidraw.Open()
			if err != nil {
				a.setOpenError(err)
				if !a.sleep(a.opt.RetryEvery) {
					return
				}
				continue
			}
			a.setOpened(opened)
			dev = opened
		}

		n, err := dev.ReadReport(buf, a.opt.PollTimeout)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// Nothing arrived, which is normal between heartbeats - but if it
				// keeps happening the reading on screen is ageing, and the whole
				// point of the timestamp is lost if the age is only recalculated
				// when a new frame arrives. So it is re-evaluated here.
				a.noteSilence()
				continue
			}
			if a.stopping() {
				// Shutdown closed the device out from under this read. That is
				// the intended way to wake the loop, so it is not an error.
				return
			}
			a.log("read failed: %v", err)
			a.closeDevice(dev)
			a.setDisconnected(err)
			if !a.sleep(a.opt.RetryEvery) {
				return
			}
			continue
		}

		a.handleReport(buf[:n])
	}
}

// stopping reports whether shutdown has begun. It is checked before treating a
// failed read as a lost device, because closing the device is how shutdown
// interrupts a blocked read.
func (a *app) stopping() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

func (a *app) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-a.done:
		return false
	}
}

func (a *app) currentDevice() *hidraw.Device {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dev
}

func (a *app) setOpened(dev *hidraw.Device) {
	a.mu.Lock()
	a.dev = dev
	a.devicePath = dev.Path()
	a.connected = true
	a.lastErr = nil
	a.mu.Unlock()
	a.log("opened %s", dev.Path())
	a.refresh()
}

func (a *app) closeDevice(dev *hidraw.Device) {
	a.mu.Lock()
	if a.dev == dev {
		a.dev = nil
	}
	a.connected = false
	a.mu.Unlock()
	dev.Close()
}

func (a *app) setOpenError(err error) {
	a.mu.Lock()
	changed := a.lastErr == nil || a.lastErr.Error() != err.Error()
	a.lastErr = err
	a.connected = false
	a.mu.Unlock()
	if changed {
		a.log("cannot open device: %v", err)
	}
	a.refresh()
}

func (a *app) setDisconnected(err error) {
	a.mu.Lock()
	a.lastErr = err
	a.connected = false
	a.mu.Unlock()
	a.refresh()
}

func (a *app) handleReport(report []byte) {
	if battery, ok := mouse.ParseBattery(report); ok {
		now := time.Now()

		a.mu.Lock()
		changed := !a.haveBattery || a.battery != battery
		var silent time.Duration
		if a.haveBattery && !a.batterySeen.IsZero() {
			silent = now.Sub(a.batterySeen)
		}
		a.battery = battery
		a.haveBattery = true
		a.batterySeen = now
		a.silenceLogged = false
		a.mu.Unlock()

		if silent >= quietAfter {
			a.log("battery reports resumed after %s", formatAge(silent))
		}
		if changed {
			a.log("battery %s", battery)
			a.maybeAlert(battery)
		}
		a.refresh()
		return
	}

	if stage, ok := mouse.ParseStage(report); ok {
		a.mu.Lock()
		a.stage = stage
		a.haveStage = true
		a.stageConfirmed = true
		a.mu.Unlock()

		a.log("dpi %s", stage)
		a.saveStage(stage.Stage)
		a.refresh()
		return
	}

	// The receiver multiplexes other HID traffic onto this channel; ignore it.
	if a.opt.Verbose {
		a.log("ignored report % x", report)
	}
}

// maybeAlert posts battery notifications, at most once per threshold per
// discharge cycle.
func (a *app) maybeAlert(battery mouse.Battery) {
	charging := battery.Charge == mouse.ChargeCharging

	a.mu.Lock()
	var message string
	switch {
	case charging:
		// Re-arm the warnings so unplugging warns again.
		a.alerts.low20 = false
		a.alerts.low10 = false
		// The mouse reports "charging" and a percentage, with no distinct full
		// state, so this is a heuristic rather than a reported fact.
		if battery.Percent >= 99 && !a.alerts.full {
			a.alerts.full = true
			message = "Battery is full"
		}
	default:
		a.alerts.full = false
		switch {
		case battery.Percent <= 10 && !a.alerts.low10:
			a.alerts.low10 = true
			a.alerts.low20 = true
			message = fmt.Sprintf("Battery is low: %d %% left", battery.Percent)
		case battery.Percent <= 20 && !a.alerts.low20:
			a.alerts.low20 = true
			message = fmt.Sprintf("Battery is getting low: %d %%", battery.Percent)
		}
	}
	notifier := a.notifier
	a.mu.Unlock()

	if message == "" {
		return
	}
	a.log("alert: %s", message)
	if err := notifier.Notify("Attack Shark X11", message); err != nil {
		// Not permanent: a desktop whose notification daemon starts late, or
		// has just been restarted, should still get this warning. One retry
		// only, because the latched alert means the next threshold crossing is
		// the natural next attempt, and the notifier itself refuses to post
		// again until its retry window has passed.
		a.log("notification failed: %v, retrying in %s", err, notify.RetryInterval)
		time.AfterFunc(notify.RetryInterval, func() {
			if err := notifier.Notify("Attack Shark X11", message); err != nil {
				a.log("notification retry failed: %v", err)
			}
		})
	}
}

func (a *app) setStatus(message string) {
	if a.itemStatus != nil {
		a.itemStatus.SetTitle(message)
	}
}

// noteSilence runs when a read timed out with nothing in it, so the age of the
// reading on screen keeps climbing even though no report arrived.
func (a *app) noteSilence() {
	a.mu.Lock()
	age := a.batteryAgeLocked()
	readings := a.battery
	haveBattery := a.haveBattery
	logIt := haveBattery && age >= quietAfter && !a.silenceLogged
	if logIt {
		a.silenceLogged = true
	}
	a.mu.Unlock()

	if logIt {
		a.log("no battery reports for %s; last read %s", formatAge(age), readings)
	}
	a.refresh()
}

// batteryAgeLocked is how long ago the battery reading arrived. A missing
// timestamp is reported as zero, i.e. current, because inventing an age would be
// worse than saying nothing.
func (a *app) batteryAgeLocked() time.Duration {
	if a.batterySeen.IsZero() {
		return 0
	}
	if age := time.Since(a.batterySeen); age > 0 {
		return age
	}
	return 0
}

// batteryPhrase describes the reading and, once reports stop arriving, how old
// it is. A frozen number is never presented as a live one.
func batteryPhrase(reading string, age time.Duration) string {
	switch {
	case age < freshFor:
		return reading
	case age < quietAfter:
		return fmt.Sprintf("%s, last read %s ago", reading, formatAge(age))
	default:
		return fmt.Sprintf("no reports for %s; last read %s", formatAge(age), reading)
	}
}

// formatAge renders a duration the way someone reading a tooltip wants it:
// seconds up to a minute, whole minutes up to an hour, then hours and minutes.
func formatAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%d s", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%d min", int(age.Minutes()))
	default:
		hours := int(age.Hours())
		minutes := int(age.Minutes()) % 60
		if minutes == 0 {
			return fmt.Sprintf("%d h", hours)
		}
		return fmt.Sprintf("%d h %d min", hours, minutes)
	}
}

// setTitle replaces a label only when the text actually changed, so a freshness
// that ticks every second does not rewrite the same string over D-Bus.
func setTitle(item *systray.MenuItem, last *string, title string) {
	if item == nil || *last == title {
		return
	}
	item.SetTitle(title)
	*last = title
}

// refresh redraws the icon, tooltip and menu from the current state. The whole
// body runs under the mutex so that iconKey and the reported state cannot be
// observed half-updated.
func (a *app) refresh() {
	a.mu.Lock()
	defer a.mu.Unlock()

	state := batteryicon.State{
		Connected: a.connected,
		Percent:   a.battery.Percent,
		Charging:  a.battery.Charge == mouse.ChargeCharging,
	}
	if key := state.Key(); key != a.iconKey {
		png, err := batteryicon.PNG(state, a.opt.IconSize)
		if err != nil {
			a.log("could not render icon: %v", err)
		} else {
			systray.SetIcon(png)
			a.iconKey = key
		}
	}

	if text := a.tooltipLocked(); text != a.lastTooltip {
		systray.SetTooltip(text)
		a.lastTooltip = text
	}

	if a.itemBattery != nil {
		title := "Battery: waiting"
		if a.haveBattery {
			title = fmt.Sprintf("Battery: %s", batteryPhrase(a.battery.String(), a.batteryAgeLocked()))
		}
		setTitle(a.itemBattery, &a.lastBatteryText, title)
	}

	if a.itemStage != nil {
		switch {
		case !a.haveStage:
			a.itemStage.SetTitle("DPI: unknown")
		case !a.stageConfirmed:
			a.itemStage.SetTitle(fmt.Sprintf("DPI: stage %d (last seen)", a.stage.Stage))
		default:
			a.itemStage.SetTitle(a.stageTitleLocked())
		}
	}

	if a.itemStatus != nil {
		var title string
		switch {
		case a.connected && a.batteryAgeLocked() >= quietAfter:
			// The device is there and open, so the silence is about the mouse.
			title = fmt.Sprintf("No reports for %s (device open at %s)", formatAge(a.batteryAgeLocked()), a.devicePath)
		case a.connected:
			title = fmt.Sprintf("Connected via %s", a.devicePath)
		case a.lastErr != nil:
			title = shorten(a.lastErr.Error(), 70)
		default:
			title = "Not connected"
		}
		setTitle(a.itemStatus, &a.lastStatusText, title)
	}

	// The mouse only announces a stage when it changes, so a box can only be
	// ticked for a stage that has actually been confirmed this run.
	for i, item := range a.stageItems {
		if a.haveStage && a.stageConfirmed && a.stage.Stage == i+1 {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

func (a *app) stageTitleLocked() string {
	if desc := a.opt.DPI.StageDescription(a.stage.Stage); desc != "" {
		return fmt.Sprintf("DPI: %s (stage %d of %d)", desc, a.stage.Stage, mouse.MaxStage)
	}
	return fmt.Sprintf("DPI: stage %d of %d", a.stage.Stage, mouse.MaxStage)
}

func (a *app) tooltipLocked() string {
	if !a.connected {
		return "Attack Shark X11 - not connected, waiting for the receiver"
	}
	parts := []string{"Attack Shark X11"}
	if a.haveBattery {
		parts = append(parts, batteryPhrase(a.battery.String(), a.batteryAgeLocked()))
	} else {
		parts = append(parts, "battery: waiting for the first report")
	}
	switch {
	case !a.haveStage:
		parts = append(parts, "DPI stage unknown (tap the mouse's DPI button to sync)")
	case !a.stageConfirmed:
		parts = append(parts, a.lastSeenStageLocked())
	default:
		parts = append(parts, a.stagePhraseLocked())
	}
	return strings.Join(parts, " - ")
}

func (a *app) lastSeenStageLocked() string {
	if desc := a.opt.DPI.StageDescription(a.stage.Stage); desc != "" {
		return fmt.Sprintf("DPI stage %d of %d, %s (last seen)", a.stage.Stage, mouse.MaxStage, desc)
	}
	return fmt.Sprintf("DPI stage %d of %d (last seen)", a.stage.Stage, mouse.MaxStage)
}

func (a *app) stagePhraseLocked() string {
	if desc := a.opt.DPI.StageDescription(a.stage.Stage); desc != "" {
		return fmt.Sprintf("DPI stage %d of %d (%s)", a.stage.Stage, mouse.MaxStage, desc)
	}
	return fmt.Sprintf("DPI stage %d of %d", a.stage.Stage, mouse.MaxStage)
}

// shorten collapses whitespace and truncates to at most max bytes, including
// the ellipsis it appends.
func shorten(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// --- stage persistence ---------------------------------------------------

func (a *app) stageFilePath() string {
	if a.opt.StateDir == "" {
		return ""
	}
	return filepath.Join(a.opt.StateDir, "stage")
}

func (a *app) loadStage() {
	path := a.stageFilePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	stage := mouse.Stage{Stage: n}
	if !stage.Valid() {
		return
	}
	a.stage = stage
	a.haveStage = true
	// Remembered, not observed, so it is shown as "last seen" until the mouse
	// confirms it.
	a.stageConfirmed = false
}

func (a *app) saveStage(stage int) {
	path := a.stageFilePath()
	if path == "" {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(stage)+"\n"), 0o600); err != nil {
		return
	}
	os.Rename(tmp, path)
}

func (a *app) clearSavedStage() {
	if path := a.stageFilePath(); path != "" {
		os.Remove(path)
	}
}
