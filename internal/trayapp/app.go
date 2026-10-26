// Package trayapp is the Attack Shark X11 tray application.
//
// One goroutine owns the device file descriptor and reads the receiver's status
// channel; it drives the tray icon, tooltip and menu from what arrives. The app
// changes three settings, and only when asked: clicking a stage writes the DPI
// configuration back with just that stage selected, clicking a DPI value writes
// it back with that stage's value changed, and clicking a rate writes the
// polling-rate report. Reading the per-stage values needs the 0xA0 permission
// frame. Those three feature writes are the app's only writes; the rest of what
// it shows comes from reports the mouse pushes, plus the selected stage and
// polling rate that the startup reads carry. The firmware refuses those reads in
// bursts, so a refused one is asked for again a few times before the value is
// left unknown (startupread.go).
//
// The selected stage arrives two ways: the configuration read carries it, so the
// label is right from the moment the device opens, and the mouse announces it
// afterwards whenever the stage changes. The DPI value behind each stage is
// stored in the mouse and only readable through a two-step permission handshake
// on the vendor feature reports (see mouse.ReadDPIConfig).
//
// Nothing is guessed: when the read fails - older firmware, the wrong HID
// collection, a mouse that is asleep - the stage and its value stay unlabelled
// until the device reports them, because an invented number is worse than a
// blank.
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
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"

	"attackshark-x11/internal/batteryicon"
	"attackshark-x11/internal/hidraw"
	"attackshark-x11/internal/mouse"
	"attackshark-x11/internal/notify"
	"attackshark-x11/internal/pointerspeed"
)

// Options configures the application.
type Options struct {
	// IconSize is the tray icon edge length in pixels. The panel scales it, so
	// this only needs to be large enough for a crisp glyph.
	IconSize int
	// ReadDPI asks the mouse for the DPI values its stages are actually set to,
	// the stage it has selected and the polling rate it reports at, when the
	// device is opened. Each needs one 0xA0 permission request, which changes no
	// setting. Switching a stage or a rate reads the device regardless; this flag
	// only controls the startup reads the labels come from. Turning it off is a
	// way to keep the app from talking to a device that refuses reads at open,
	// at the cost of the values and the stage being unknown until they change.
	ReadDPI bool
	// PollTimeout bounds each device read. It is also how often the loop wakes
	// up to notice a vanished device.
	PollTimeout time.Duration
	// RetryEvery is how long to wait between attempts to open the device.
	RetryEvery time.Duration
	// NoNotify disables desktop notifications.
	NoNotify bool
	// Verbose logs device activity to stderr.
	Verbose bool
}

// DefaultOptions returns the options the tray command starts from.
func DefaultOptions() Options {
	return Options{
		IconSize:    24,
		ReadDPI:     true,
		PollTimeout: time.Second,
		RetryEvery:  3 * time.Second,
	}
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
	dev        *hidraw.Device
	devicePath string
	// deviceDPI holds the values read from the mouse; the menus and tooltips
	// label stages from it, and a confirmed write replaces it.
	deviceDPI mouse.DPIStages
	// stageRead and pollingRead retry the reads made when the device was opened;
	// see startupRead. They are owned by the device goroutine.
	stageRead   startupRead
	pollingRead startupRead
	// requests carries a click on a setting to the goroutine that owns the
	// device. It is buffered so a click never blocks the UI.
	requests chan settingRequest
	// busy is set while a request is being written, before the confirmation
	// window opens.
	busy bool
	// pending is the write the device has not resolved yet, nil when nothing is
	// in flight. The device processes one feature report at a time, so one slot
	// covers every setting: two writes at once would make a 0x50 reply
	// ambiguous.
	pending     *pendingChange
	lastErr     error
	connected   bool
	battery     mouse.Battery
	haveBattery bool
	// batteryPrimed is set once a battery frame has been accepted since the
	// device was opened. The first frame is held back rather than shown; see
	// acceptBatteryLocked.
	batteryPrimed bool
	stage         mouse.Stage
	haveStage     bool
	// polling is the rate the device was set to when it was opened, or the one
	// it confirmed afterwards. The mouse does not announce this setting, so a
	// change made by another program is only noticed on the next open.
	polling     mouse.PollingRate
	havePolling bool
	alerts      alerts
	iconKey     string
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
	lastPollingText string

	done chan struct{}

	// menu items
	itemBattery   *systray.MenuItem
	itemStage     *systray.MenuItem
	itemStatus    *systray.MenuItem
	itemDPI       *systray.MenuItem
	stageItems    []*systray.MenuItem
	itemPolling   *systray.MenuItem
	pollingItems  []*systray.MenuItem
	itemDPIValue  *systray.MenuItem
	dpiValueItems []*systray.MenuItem

	// pointer is the desktop's pointer-speed setting, nil when this desktop
	// keeps it somewhere this app cannot write. pointerMu covers the gsettings
	// call, which must not run under a.mu: refresh takes that on every tick.
	pointer         pointerSpeedBackend
	pointerMu       sync.Mutex
	pointerValue    float64
	havePointer     bool
	itemPointer     *systray.MenuItem
	pointerItems    []*systray.MenuItem
	lastPointerText string
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

	a := &app{
		opt:      opt,
		done:     make(chan struct{}),
		requests: make(chan settingRequest, 1),
	}
	if opt.ReadDPI {
		a.log("will read the DPI values, the selected stage and the polling rate from the device when it opens")
	} else {
		a.log("device reads disabled; the stage and polling rate stay unknown until they change")
	}
	if !opt.NoNotify {
		if notifier, err := notify.New(); err == nil {
			a.notifier = notifier
		} else {
			a.log("notifications disabled: %v", err)
		}
	}
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

	// The six stages, each of which switches the mouse when clicked. The tick
	// follows what the mouse reports, not what was clicked, so an unconfirmed or
	// rejected change never looks applied.
	// The labels start plain because the values have not been read yet; the
	// read rewrites them through refreshDPIMenu once the device answers.
	a.itemDPI = systray.AddMenuItem("DPI stages", a.dpiMenuTooltip())
	for i := 1; i <= mouse.MaxStage; i++ {
		title := fmt.Sprintf("Stage %d", i)
		if desc := a.deviceDPI.StageDescription(i); desc != "" {
			title = fmt.Sprintf("Stage %d - %s", i, desc)
		}
		item := a.itemDPI.AddSubMenuItemCheckbox(title, a.stageItemTooltip(i), false)
		a.stageItems = append(a.stageItems, item)
	}

	// The polling rate, with the same rule as the stages: the tick follows the
	// device's confirmation, never the click.
	a.itemPolling = systray.AddMenuItem("Polling rate", a.pollingMenuTooltip())
	for _, rate := range mouse.PollingRates {
		title := fmt.Sprintf("%s - %s", rate, rate.Name())
		item := a.itemPolling.AddSubMenuItemCheckbox(title, pollingItemTooltip(rate), false)
		a.pollingItems = append(a.pollingItems, item)
	}

	// The DPI value of the stage the mouse is on. The steps are coarse on
	// purpose: this is a tray menu, not the vendor's editor, and every step is
	// a value the sensor stores exactly.
	a.itemDPIValue = systray.AddMenuItem("DPI value", dpiValueMenuTooltip())
	for _, value := range dpiValueSteps {
		item := a.itemDPIValue.AddSubMenuItemCheckbox(
			fmt.Sprintf("%d DPI", value), dpiValueItemTooltip(value), false)
		a.dpiValueItems = append(a.dpiValueItems, item)
	}

	// The desktop's pointer speed. Not a mouse setting: the sensor's DPI is how
	// far the mouse travels per inch, while this is how far the cursor moves
	// for it. It lives in the desktop's own settings, so it also works with the
	// receiver unplugged, and the tick follows what the desktop reports.
	if backend, ok := pointerspeed.Detect(); ok {
		a.pointer = backend
		a.itemPointer = systray.AddMenuItem("Pointer speed",
			pointerMenuTooltipText(backend.Desktop(), 0, false))
		for _, value := range pointerspeed.Steps {
			item := a.itemPointer.AddSubMenuItemCheckbox(
				pointerspeed.Format(value), pointerItemTooltip(value), false)
			a.pointerItems = append(a.pointerItems, item)
		}
		go a.startPointerSpeed()
		go a.watchPointerSpeedClicks()
	} else {
		// No schema to write: say so once, rather than offering values that
		// nothing on this desktop would read.
		a.itemPointer = systray.AddMenuItem("Pointer speed: not available on this desktop",
			"Changing the pointer speed needs the Cinnamon or GNOME mouse settings, and this desktop has neither.")
		a.itemPointer.Disable()
	}

	systray.AddSeparator()
	a.itemStatus = systray.AddMenuItem("starting up", "Connection state")
	a.itemStatus.Disable()

	reconnect := systray.AddMenuItem("Reconnect now", "Try to open the receiver again")

	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Stop the tray application")

	go a.deviceLoop()
	go a.eventLoop(reconnect, quit)
	go a.watchSettingClicks()

	a.refresh()
}

// dpiStagesLocked returns the values stages are labelled with: what the mouse
// reported, and what a confirmed change replaced it with. Callers hold a.mu.
func (a *app) dpiStagesLocked() mouse.DPIStages {
	return a.deviceDPI
}

// dpiMenuTooltip explains what the listed DPI values are and where they came
// from, which is the device and nowhere else.
func (a *app) dpiMenuTooltip() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return dpiMenuTooltipText(a.dpiStagesLocked())
}

func dpiMenuTooltipText(stages mouse.DPIStages) string {
	base := fmt.Sprintf("The mouse's %d DPI stages and the DPI value set for each. "+
		"Click one to switch the mouse to it.", mouse.MaxStage)
	if !stages.Configured() {
		base += fmt.Sprintf(" The values have not been read from the mouse yet; the %s sensor supports up to %d DPI.",
			mouse.SensorName, mouse.MaxDPI)
	}
	return base
}

func (a *app) stageItemTooltip(stage int) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return stageItemTooltipText(a.dpiStagesLocked(), stage)
}

// dpiValueSteps are the values the DPI value menu offers: 2000 DPI up to the
// sensor's maximum, in 2000 DPI steps. Every one of them is a value the sensor
// stores exactly, which is what makes the menu a setting rather than a guess.
var dpiValueSteps = func() []int {
	var values []int
	for value := 2000; value <= mouse.MaxDPI; value += 2000 {
		values = append(values, value)
	}
	return values
}()

// dpiValueMenuTooltip says which stage a picked value applies to, because that
// is the part a menu of numbers cannot show.
func dpiValueMenuTooltip() string {
	return "The DPI value of the stage the mouse is on. Click one to set it; the other stages " +
		"are left alone, and the tick follows the device's confirmation."
}

func dpiValueItemTooltip(value int) string {
	return fmt.Sprintf("Set the stage the mouse is on to %d DPI.", value)
}

// pollingMenuTooltip explains what the polling rate is and where its value came
// from, because "the mouse is set to 1000 Hz" is only useful if it is true.
func (a *app) pollingMenuTooltip() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return pollingMenuTooltipText(a.polling, a.havePolling)
}

func pollingMenuTooltipText(rate mouse.PollingRate, known bool) string {
	value := "The rate the mouse is set to has not been read."
	if known {
		value = fmt.Sprintf("The mouse is set to %s.", rate)
	}
	return fmt.Sprintf("%s How often it reports to the computer. Click a rate to switch the mouse to it; "+
		"the tick follows the device's confirmation.", value)
}

// pollingItemTooltip says what clicking one rate does. It does not claim to
// confirm the current rate: clicking the rate the device already reports is a
// no-op rather than a fresh read.
func pollingItemTooltip(rate mouse.PollingRate) string {
	return fmt.Sprintf("Switch the mouse to %s (%s).", rate, rate.Name())
}

func stageItemTooltipText(stages mouse.DPIStages, stage int) string {
	if desc := stages.StageDescription(stage); desc != "" {
		return fmt.Sprintf("Stage %d is set to %s. Click to switch the mouse to this stage.",
			stage, desc)
	}
	return fmt.Sprintf("Stage %d. Click to switch the mouse to this stage; its DPI value has not been read.", stage)
}

// settingKind names which setting a queued request changes.
type settingKind int

const (
	settingStage settingKind = iota + 1
	settingPolling
	settingDPI
)

// settingRequest is a change the user asked for, queued for the goroutine that
// owns the device. Exactly one of the value fields is used, chosen by kind.
// settingDPI fills stage when it is queued, because the stage the value applies
// to is the one the mouse is on at that moment.
type settingRequest struct {
	kind  settingKind
	stage int
	rate  mouse.PollingRate
	value int
}

// label describes the request in log lines and notifications.
func (r settingRequest) label() string {
	switch r.kind {
	case settingPolling:
		return r.rate.String() + " polling"
	case settingDPI:
		return fmt.Sprintf("%d DPI on stage %d", r.value, r.stage)
	default:
		return fmt.Sprintf("stage %d", r.stage)
	}
}

// report is the feature report the device must name in its 0x50 reply.
func (r settingRequest) report() byte {
	if r.kind == settingPolling {
		return mouse.PollingReportID
	}
	return mouse.DPIReportID
}

// pendingChange is a write the device has not resolved yet.
type pendingChange struct {
	kind   settingKind
	report byte
	stage  int
	rate   mouse.PollingRate
	value  int
	// dpi is the configuration the DPI write sent, kept until the device
	// confirms it: only then is it what the labels describe.
	dpi *mouse.DPIConfig
	// deadline bounds the wait for the 0x50 reply.
	deadline time.Time
}

func (p pendingChange) label() string {
	return settingRequest{kind: p.kind, stage: p.stage, rate: p.rate, value: p.value}.label()
}

// changeOutcome is what a requested change resolved to.
type changeOutcome struct {
	change  pendingChange
	success bool
	// reason is "acknowledged", "read back", "rejected" or "timeout".
	reason string
}

// watchSettingClicks turns a click on a menu entry into a request. Each entry
// gets its own goroutine because a systray menu has no single event stream; the
// request itself is what serialises access to the device.
func (a *app) watchSettingClicks() {
	for i, item := range a.stageItems {
		go a.watchClicks(item, settingRequest{kind: settingStage, stage: i + 1})
	}
	for i, item := range a.pollingItems {
		go a.watchClicks(item, settingRequest{kind: settingPolling, rate: mouse.PollingRates[i]})
	}
	for i, item := range a.dpiValueItems {
		go a.watchClicks(item, settingRequest{kind: settingDPI, value: dpiValueSteps[i]})
	}
}

func (a *app) watchClicks(item *systray.MenuItem, req settingRequest) {
	for {
		select {
		case <-a.done:
			return
		case _, ok := <-item.ClickedCh:
			if !ok {
				return
			}
			a.requestChange(req)
		}
	}
}

// requestChange queues a change for the device goroutine, refusing rather than
// queueing when a change is already in flight.
func (a *app) requestChange(req settingRequest) {
	a.mu.Lock()
	connected := a.connected
	busy := a.busy || a.pending != nil
	stageKnown := a.haveStage
	if req.kind == settingDPI {
		// The value applies to the stage the mouse is on right now, not to one
		// chosen when the menu was built.
		req.stage = a.stage.Stage
	}
	alreadySet := a.atTargetLocked(req)
	a.mu.Unlock()

	switch {
	case !connected:
		a.notifyChange("Cannot change the mouse's settings: the receiver is not open")
		return
	case req.kind == settingDPI && !stageKnown:
		a.notifyChange("Cannot set the DPI value: the mouse has not reported which stage it is on")
		return
	case busy:
		a.notifyChange("Still waiting for the mouse to confirm the previous change")
		return
	case alreadySet:
		return
	}

	select {
	case a.requests <- req:
	default:
		// A click that arrives while another is queued is dropped on purpose:
		// two writes at once would make the acknowledgement ambiguous.
	}
}

// atTargetLocked reports whether the setting already holds the requested value
// according to the last thing the device said. Callers hold a.mu.
func (a *app) atTargetLocked(req settingRequest) bool {
	switch req.kind {
	case settingPolling:
		return a.havePolling && a.polling == req.rate
	case settingDPI:
		value, ok := a.dpiStagesLocked().At(req.stage)
		return ok && value == req.value
	default:
		return a.haveStage && a.stage.Stage == req.stage
	}
}

// handleRequest serves at most one queued click per device-loop iteration.
func (a *app) handleRequest(dev *hidraw.Device) {
	select {
	case req := <-a.requests:
		a.applyChange(dev, req)
	default:
	}
}

// applyChange performs the write and opens the confirmation window. It runs on
// the device goroutine; status reads resume as soon as the write returns, which
// is what lets the 0x50 reply be matched.
func (a *app) applyChange(dev *hidraw.Device, req settingRequest) {
	a.mu.Lock()
	if a.busy || a.pending != nil {
		a.mu.Unlock()
		a.log("change to %s dropped: another change is in flight", req.label())
		return
	}
	a.busy = true
	a.mu.Unlock()

	var err error
	var dpiCfg *mouse.DPIConfig
	switch req.kind {
	case settingPolling:
		_, err = mouse.ApplyPollingRate(dev, req.rate)
	case settingDPI:
		var cfg mouse.DPIConfig
		cfg, err = mouse.ApplyDPIValue(dev, req.stage, req.value)
		if err == nil {
			dpiCfg = &cfg
		}
	default:
		_, err = mouse.ApplyStage(dev, req.stage)
	}

	a.mu.Lock()
	a.busy = false
	if err == nil {
		a.pending = &pendingChange{
			kind:     req.kind,
			report:   req.report(),
			stage:    req.stage,
			rate:     req.rate,
			value:    req.value,
			dpi:      dpiCfg,
			deadline: time.Now().Add(mouse.ACKTimeout),
		}
	}
	a.mu.Unlock()

	if err != nil {
		a.log("change to %s failed: %v", req.label(), err)
		a.notifyChange(fmt.Sprintf("Could not set %s: %s", req.label(), shorten(err.Error(), 120)))
		a.refresh()
		return
	}
	a.log("asked the mouse for %s; waiting up to %s for confirmation", req.label(), mouse.ACKTimeout)
}

// noteCommandConfirmation routes a 0x50 reply to the pending change, if it is the
// one being waited for.
func (a *app) noteCommandConfirmation(conf mouse.CommandConfirmation) {
	outcome, ok := a.resolveConfirmation(conf)
	if !ok {
		a.log("ignored confirmation for report 0x%02x (status 0x%02x)", conf.ReportID, conf.Status)
		return
	}
	a.reportOutcome(outcome)
}

// resolveConfirmation consumes a matching confirmation and updates the state the
// UI is drawn from. It is the pure part of the handshake, tested without a tray.
func (a *app) resolveConfirmation(conf mouse.CommandConfirmation) (changeOutcome, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.pending == nil || conf.ReportID != a.pending.report {
		return changeOutcome{}, false
	}

	p := *a.pending
	a.pending = nil

	if !conf.Success {
		return changeOutcome{change: p, reason: "rejected"}, true
	}
	a.applyConfirmedLocked(p)
	return changeOutcome{change: p, success: true, reason: "acknowledged"}, true
}

// applyConfirmedLocked records a value the device has confirmed. Callers hold
// a.mu.
func (a *app) applyConfirmedLocked(p pendingChange) {
	switch p.kind {
	case settingPolling:
		a.polling = p.rate
		a.havePolling = true
	case settingDPI:
		// The configuration was read before the write and changed in one field,
		// so all six values can now be labelled as read from the device.
		if p.dpi != nil {
			a.deviceDPI = p.dpi.Stages
		}
	default:
		a.stage = mouse.Stage{Stage: p.stage}
		a.haveStage = true
	}
}

// resolveTimeout consumes the pending change once its window has closed.
func (a *app) resolveTimeout(now time.Time) (pendingChange, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.pending == nil || now.Before(a.pending.deadline) {
		return pendingChange{}, false
	}
	p := *a.pending
	a.pending = nil
	return p, true
}

// expireRequest resolves a change whose confirmation window has closed. A
// missing 0x50 reply is not proof of failure - measured on hardware, the
// confirmation is sometimes not delivered although the write applied - so the
// setting is read back and compared before the change is called a timeout.
func (a *app) expireRequest(dev *hidraw.Device) {
	p, ok := a.resolveTimeout(time.Now())
	if !ok {
		return
	}
	outcome := changeOutcome{change: p, reason: "timeout"}
	if dev != nil && a.confirmByRead(dev, p) {
		outcome = changeOutcome{change: p, success: true, reason: "read back"}
	}
	a.reportOutcome(outcome)
}

// confirmByRead asks the device whether the setting now holds the written value.
// A read that fails is not a contradiction, so it leaves the outcome a timeout.
func (a *app) confirmByRead(dev *hidraw.Device, p pendingChange) bool {
	if p.kind == settingDPI {
		cfg, err := mouse.ReadDPIConfig(dev)
		if err != nil {
			a.log("could not verify %d DPI on stage %d by reading it back: %v", p.value, p.stage, err)
			return false
		}
		value, known := cfg.Stages.At(p.stage)
		if !known || value != p.value {
			a.log("read back %d DPI on stage %d after asking for %d", value, p.stage, p.value)
			return false
		}
		a.mu.Lock()
		a.deviceDPI = cfg.Stages
		a.mu.Unlock()
		return true
	}

	if p.kind == settingPolling {
		cfg, err := mouse.ReadPollingRate(dev)
		if err != nil {
			a.log("could not verify the polling rate by reading it back: %v", err)
			return false
		}
		if cfg.Rate != p.rate {
			a.log("read back %s after asking for %s", cfg.Rate, p.rate)
			return false
		}
		a.mu.Lock()
		a.polling = p.rate
		a.havePolling = true
		a.mu.Unlock()
		return true
	}

	cfg, err := mouse.ReadDPIConfig(dev)
	if err != nil {
		a.log("could not verify the stage change to %d by reading it back: %v", p.stage, err)
		return false
	}
	if cfg.ActiveStage != p.stage {
		a.log("read back stage %d after asking for %d", cfg.ActiveStage, p.stage)
		return false
	}

	a.mu.Lock()
	a.stage = mouse.Stage{Stage: p.stage}
	a.haveStage = true
	a.mu.Unlock()
	return true
}

// reportOutcome turns a resolved change into the log line, the notification and
// the redraw. Only a confirmation moves the tick.
func (a *app) reportOutcome(outcome changeOutcome) {
	p := outcome.change
	switch {
	case outcome.success:
		a.log("%s confirmed (%s)", p.label(), outcome.reason)
	case outcome.reason == "timeout":
		a.log("no confirmation for %s within %s", p.label(), mouse.ACKTimeout)
		a.notifyChange(fmt.Sprintf("The mouse did not confirm the change to %s", p.label()))
	default:
		a.log("the device rejected the change to %s", p.label())
		a.notifyChange(fmt.Sprintf("The mouse rejected the change to %s", p.label()))
	}
	if outcome.success && p.kind == settingDPI {
		// The stage labels carry the value that just changed.
		a.refreshDPIMenu()
	}
	a.refresh()
}

// notifyChange posts a message about a setting change. A tray click gives no
// other feedback, so this is how a refusal becomes visible.
func (a *app) notifyChange(message string) {
	if a.notifier == nil {
		return
	}
	if err := a.notifier.Notify("Attack Shark X11", message); err != nil {
		a.log("notification failed: %v", err)
	}
}

// featureReadWriter is the part of a hidraw device the startup reads need: one
// feature write to ask, one feature read to receive. Keeping it an interface
// means the retry behaviour can be tested without hardware.
type featureReadWriter interface {
	SetFeature(buf []byte) error
	GetFeature(reportID byte, size int) ([]byte, error)
}

// readDeviceDPI asks the mouse for the values its stages are set to. Failure is
// expected rather than exceptional - older firmware, the wrong HID collection or
// a sleeping mouse all fail here - so it leaves the labels empty: a guessed
// number is worse than no number. A refusal is retried, because the firmware
// declines in bursts that pass within seconds.
func (a *app) readDeviceDPI(dev featureReadWriter) {
	if !a.opt.ReadDPI {
		return
	}
	cfg, err := mouse.ReadDPIConfig(dev)
	if err != nil {
		a.log("could not read the DPI values from the device: %v", err)
		a.noteReadFailure(&a.stageRead, "the DPI values and the selected stage")
		return
	}
	a.stageRead.succeeded()

	a.applyDeviceDPI(cfg.Stages)
	// The same report carries the stage the device has selected, so the label no
	// longer has to wait for a change announcement - or for someone to press the
	// mouse's DPI button - before it means anything.
	if !a.applyDeviceStage(cfg.ActiveStage) {
		a.log("device reports stage %d, outside 1-%d; leaving the stage unknown",
			cfg.ActiveStage, mouse.MaxStage)
	}

	a.log("DPI values read from the device: %s (profile %d, stage %d selected)",
		cfg.Stages, cfg.Profile, cfg.ActiveStage)
	a.refreshDPIMenu()
	a.refresh()
}

// readDevicePolling asks the mouse how often it reports to the host. It is the
// same kind of read as the DPI configuration - one permission request, no
// setting changed - and it is skipped with the same flag, because a machine that
// cannot be read on one report usually cannot be read on the other.
func (a *app) readDevicePolling(dev featureReadWriter) {
	if !a.opt.ReadDPI {
		return
	}
	cfg, err := mouse.ReadPollingRate(dev)
	if err != nil {
		a.log("could not read the polling rate from the device: %v", err)
		a.noteReadFailure(&a.pollingRead, "the polling rate")
		return
	}
	a.pollingRead.succeeded()

	a.mu.Lock()
	a.polling = cfg.Rate
	a.havePolling = true
	a.mu.Unlock()

	a.log("polling rate read from the device: %s (profile %d)", cfg.Rate, cfg.Profile)
	a.refresh()
}

// noteReadFailure schedules another attempt after the device refused one of the
// reads it makes when it is opened. The log says which of the two happened,
// because "the read failed" on its own does not tell anyone whether the tray is
// going to try again.
func (a *app) noteReadFailure(read *startupRead, what string) {
	now := time.Now()
	if !read.failed(now) {
		a.log("%s stay unknown until the receiver is reopened", what)
		return
	}
	a.log("%s will be asked for again in %s", what, read.next.Sub(now).Round(time.Second))
}

// retryStartupReads asks again for a value the device refused when it was
// opened. It runs on the goroutine that owns the file descriptor, before that
// goroutine blocks on the status channel, so a retry cannot race a status read.
func (a *app) retryStartupReads(dev featureReadWriter) {
	now := time.Now()
	if a.stageRead.due(now) {
		a.readDeviceDPI(dev)
	}
	if a.pollingRead.due(now) {
		a.readDevicePolling(dev)
	}
}

// applyDeviceStage records the stage the device's configuration reports as the
// current one. Values outside the mouse's range are refused rather than shown, so
// a nonsensical byte cannot confirm a stage that was never observed.
func (a *app) applyDeviceStage(stage int) bool {
	if stage < 1 || stage > mouse.MaxStage {
		return false
	}
	a.mu.Lock()
	a.stage = mouse.Stage{Stage: stage}
	a.haveStage = true
	a.mu.Unlock()
	return true
}

// applyDeviceDPI records the values read from the mouse, which are the only
// source the menus and tooltips label stages with. An empty mapping is ignored,
// so a successful read that decoded nothing cannot blank out values that are
// already on screen.
func (a *app) applyDeviceDPI(stages mouse.DPIStages) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !stages.Configured() {
		return
	}
	a.deviceDPI = stages
}

// refreshDPIMenu rewrites the stage labels after the source of the values has
// changed. A nil menu means this ran before the tray was built, which is not an
// error: the labels are created from the same helpers when it is.
func (a *app) refreshDPIMenu() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.itemDPI == nil {
		return
	}

	stages := a.dpiStagesLocked()
	a.itemDPI.SetTooltip(dpiMenuTooltipText(stages))
	for i, item := range a.stageItems {
		stage := i + 1
		title := fmt.Sprintf("Stage %d", stage)
		if desc := stages.StageDescription(stage); desc != "" {
			title = fmt.Sprintf("Stage %d - %s", stage, desc)
		}
		item.SetTitle(title)
		item.SetTooltip(stageItemTooltipText(stages, stage))
	}
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

func (a *app) eventLoop(reconnect, quit *systray.MenuItem) {
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
				// Nothing left to verify against: report a pending change now.
				a.expireRequest(nil)
				if !a.sleep(a.opt.RetryEvery) {
					return
				}
				continue
			}
			a.setOpened(opened)
			// A device that has just been opened gets a fresh set of attempts,
			// so a receiver plugged back in is not left on an exhausted budget.
			a.stageRead = startupRead{}
			a.pollingRead = startupRead{}
			a.readDeviceDPI(opened)
			a.readDevicePolling(opened)
			dev = opened
		}

		// A read the device refused when it was opened is asked for again here,
		// before this iteration blocks on the status channel.
		a.retryStartupReads(dev)

		// A change whose confirmation window has closed is checked against the
		// device's own configuration before it is called a failure, because the
		// 0x50 reply is not always delivered even when the write landed.
		a.expireRequest(dev)

		// A click on a setting is served here, on the goroutine that owns the
		// file descriptor, so feature writes never race the status reads.
		a.handleRequest(dev)

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
	// A freshly opened device may send one wrong reading before its gauge
	// settles, so the next frame is held back rather than displayed.
	a.batteryPrimed = false
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
	if conf, ok := mouse.ParseCommandConfirmation(report); ok {
		a.noteCommandConfirmation(conf)
		return
	}

	if battery, ok := mouse.ParseBattery(report); ok {
		now := time.Now()

		a.mu.Lock()
		published, changed, silent := a.acceptBatteryLocked(battery, now)
		a.mu.Unlock()

		if !published {
			// Logged, not shown: the next heartbeat decides what the reading is.
			a.log("battery %s (first frame after connecting; waiting for the next one before showing it)", battery)
			return
		}
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
		// An announcement of the stage that was just requested is as good as
		// the acknowledgement: the mouse does not need to say it twice. A
		// pending write to another setting is untouched by it.
		announcedOurChange := a.pending != nil && a.pending.kind == settingStage &&
			a.pending.stage == stage.Stage
		if announcedOurChange {
			a.pending = nil
		}
		a.mu.Unlock()

		if announcedOurChange {
			a.log("dpi %s (announced after the requested change)", stage)
		} else {
			a.log("dpi %s", stage)
		}
		a.refresh()
		return
	}

	// The receiver multiplexes other HID traffic onto this channel; ignore it.
	if a.opt.Verbose {
		a.log("ignored report % x", report)
	}
}

// acceptBatteryLocked applies one battery frame and reports whether it should be
// shown. The first frame after the device is opened is recorded but held back:
// this firmware can report 100 % before its gauge has settled, and a wrong
// number - or the "Battery is full" alert it would trigger - is worse than the
// two seconds it takes the next heartbeat to arrive.
//
// published is false only for that first frame; changed means the reading differs
// from the one on screen; silent is how long the reports had stopped beforehand.
func (a *app) acceptBatteryLocked(battery mouse.Battery, now time.Time) (published, changed bool, silent time.Duration) {
	if !a.batteryPrimed {
		a.batteryPrimed = true
		return false, false, 0
	}

	changed = !a.haveBattery || a.battery != battery
	if a.haveBattery && !a.batterySeen.IsZero() {
		silent = now.Sub(a.batterySeen)
	}
	a.battery = battery
	a.haveBattery = true
	a.batterySeen = now
	a.silenceLogged = false
	return true, changed, silent
}

// maybeAlert posts battery notifications, at most once per threshold per
// discharge cycle.
func (a *app) maybeAlert(battery mouse.Battery) {
	a.mu.Lock()
	var message string
	switch battery.Charge {
	case mouse.ChargeFull:
		// Re-arm the warnings so unplugging warns again. This is a reported
		// fact rather than a guess: the firmware sends the 0x02 frame once,
		// when charging completes.
		a.alerts.low20 = false
		a.alerts.low10 = false
		if !a.alerts.full {
			a.alerts.full = true
			message = "Battery is full"
		}
	case mouse.ChargeCharging:
		// Re-arm the warnings so unplugging warns again.
		a.alerts.low20 = false
		a.alerts.low10 = false
		// A topped-up mouse keeps reporting "charging" with 100 %, so this is
		// the fallback for when the 0x02 frame was not delivered.
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
// that ticks every second does not rewrite the same string over D-Bus. It
// reports whether the label changed, so callers can update a tooltip alongside.
func setTitle(item *systray.MenuItem, last *string, title string) bool {
	if item == nil || *last == title {
		return false
	}
	item.SetTitle(title)
	*last = title
	return true
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
		if !a.haveStage {
			a.itemStage.SetTitle("DPI: unknown")
		} else {
			a.itemStage.SetTitle(a.stageTitleLocked())
		}
	}

	if a.itemPolling != nil {
		title := "Polling rate: unknown"
		if a.havePolling {
			title = fmt.Sprintf("Polling rate: %s", a.polling)
		}
		setTitle(a.itemPolling, &a.lastPollingText, title)
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

	// A box is ticked for the stage the device reported, or confirmed by a change.
	for i, item := range a.stageItems {
		if a.haveStage && a.stage.Stage == i+1 {
			item.Check()
		} else {
			item.Uncheck()
		}
	}

	// The polling rate is not announced at all, so its tick means "the last
	// value the device gave us", either at startup or on confirmation.
	for i, item := range a.pollingItems {
		if a.havePolling && a.polling == mouse.PollingRates[i] {
			item.Check()
		} else {
			item.Uncheck()
		}
	}

	// The DPI value menu ticks the step matching the stage the mouse is on. A
	// value that is not one of the steps (3200, say) leaves every box empty; the
	// stage line above still names it.
	currentDPI, haveDPI := 0, false
	if a.haveStage {
		currentDPI, haveDPI = a.dpiStagesLocked().At(a.stage.Stage)
	}
	for i, item := range a.dpiValueItems {
		if haveDPI && currentDPI == dpiValueSteps[i] {
			item.Check()
		} else {
			item.Uncheck()
		}
	}

	// The pointer speed is the desktop's, not the device's, so there is no
	// confirmation to wait for: the tick follows the value read back from it.
	if a.itemPointer != nil {
		switch {
		case a.pointer == nil:
			setTitle(a.itemPointer, &a.lastPointerText, "Pointer speed: not available on this desktop")
		case !a.havePointer:
			if setTitle(a.itemPointer, &a.lastPointerText, "Pointer speed: unknown") {
				a.itemPointer.SetTooltip(pointerMenuTooltipText(a.pointer.Desktop(), 0, false))
			}
		default:
			title := fmt.Sprintf("Pointer speed: %s", pointerspeed.Format(a.pointerValue))
			if setTitle(a.itemPointer, &a.lastPointerText, title) {
				a.itemPointer.SetTooltip(pointerMenuTooltipText(a.pointer.Desktop(), a.pointerValue, true))
			}
		}
	}
	for i, item := range a.pointerItems {
		if a.havePointer && a.pointerValue == pointerspeed.Steps[i] {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

func (a *app) stageTitleLocked() string {
	if desc := a.dpiStagesLocked().StageDescription(a.stage.Stage); desc != "" {
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
	if !a.haveStage {
		parts = append(parts, "DPI stage unknown (the startup read did not report the selected stage)")
	} else {
		parts = append(parts, a.stagePhraseLocked())
	}
	if a.havePolling {
		parts = append(parts, fmt.Sprintf("%s polling", a.polling))
	}
	return strings.Join(parts, " - ")
}

func (a *app) stagePhraseLocked() string {
	if desc := a.dpiStagesLocked().StageDescription(a.stage.Stage); desc != "" {
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
