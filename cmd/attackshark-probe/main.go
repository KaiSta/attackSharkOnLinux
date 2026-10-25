// Command attackshark-probe inspects the Attack Shark X11 receiver.
//
// The receiver volunteers the battery level and the DPI stage, so those need no
// writes at all. The stored DPI configuration and the polling rate have to be
// asked for: the firmware only answers after a one-shot read-permission request,
// which "dpi" and "polling" send. "set-stage" goes further, reading the
// configuration, changing the selected stage and writing the whole report back;
// "set-polling" writes the nine-byte polling-rate report. Those two are the only
// commands here that change a setting. Access needs no privileges once the udev
// rule is installed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"attackshark-x11/internal/hidraw"
	"attackshark-x11/internal/mouse"
)

const usage = `attackshark-probe inspects the Attack Shark X11 2.4 GHz receiver.

usage: attackshark-probe <command> [flags]

commands:
  info        discovered hidraw nodes, report descriptor size, permissions
  reportmap   every input/output/feature report the descriptor declares
  battery     print the battery level (waits for the next heartbeat)
  stage       print the current DPI stage
  dpi         read the stored per-stage DPI values from the device
  set-stage   switch the selected DPI stage, waiting for the device's confirmation
  polling     read the polling rate the device is set to
  set-polling switch the polling rate, waiting for the device's confirmation
  set-dpi     set the DPI value of a stage, waiting for the device's confirmation
  stream      print every status report as it arrives
  dump        hexdump every report, for reverse engineering
  doctor      check the installation: udev, device access, config, tray, session

flags are per-command; run "attackshark-probe <command> -h" for details.

notes:
  The battery heartbeat arrives every ~2s. The DPI stage is only announced
  when it changes, so "stage" returns nothing unless you tap the mouse's DPI
  button while it runs.
  "dpi" reads the values the stages are actually set to. It sends one 0xA0
  permission request first, because the firmware refuses the read otherwise;
  that request is a write on the wire but changes no setting.
  "polling" reads report 0x06 the same way, and labels it with the profile the
  device says it belongs to.
  "set-stage", "set-dpi" and "set-polling" are the commands that change a
  setting. The first two read the configuration first and write it back with
  only the selected stage, or that stage's DPI value, replaced; "set-polling"
  writes report 0x06. All three wait for the 0x50 confirmation naming the report
  they wrote.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "info":
		err = runInfo(os.Args[2:])
	case "reportmap":
		err = runReportMap(os.Args[2:])
	case "battery":
		err = runBattery(os.Args[2:])
	case "stage":
		err = runStage(os.Args[2:])
	case "dpi":
		err = runDPI(os.Args[2:])
	case "set-stage":
		err = runSetStage(os.Args[2:])
	case "polling":
		err = runPolling(os.Args[2:])
	case "set-polling":
		err = runSetPolling(os.Args[2:])
	case "set-dpi":
		err = runSetDPI(os.Args[2:])
	case "stream":
		err = runStream(os.Args[2:])
	case "dump":
		err = runDump(os.Args[2:])
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		fmt.Print(usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newFlagSet(name, blurb string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s\n\nusage: attackshark-probe %s [flags]\n\n", blurb, name)
		fs.PrintDefaults()
	}
	path := fs.String("path", "", "open a specific hidraw node instead of discovering the receiver")
	return fs, path
}

func openFlagged(path string) (*hidraw.Device, error) {
	if path != "" {
		return hidraw.OpenPath(path)
	}
	return hidraw.Open()
}

func runInfo(args []string) error {
	fs, path := newFlagSet("info", "Show what is discovered about the receiver, and whether it can be opened.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Printf("receiver      %04x:%04x\n", hidraw.VendorID, hidraw.ProductID)
	fmt.Printf("sensor        %s, up to %d DPI, %d stages\n", mouse.SensorName, mouse.MaxDPI, mouse.MaxStage)

	if _, err := os.Stat(hidraw.SymlinkPath); err == nil {
		fmt.Printf("udev symlink  %s (present)\n", hidraw.SymlinkPath)
	} else {
		fmt.Printf("udev symlink  %s (missing - install udev/99-attackshark-x11.rules)\n", hidraw.SymlinkPath)
	}

	candidates, err := hidraw.Devices()
	if err != nil {
		return err
	}
	fmt.Println("hidraw nodes:")
	for _, c := range candidates {
		mark := " "
		if c.Interface == hidraw.ConfigInterface {
			mark = "*"
		}
		fmt.Printf("  %s %-18s interface %d  %s\n", mark, c.Path, c.Interface, c.Phys)
	}
	fmt.Println("  * = vendor configuration channel (battery and DPI stage reports)")

	dev, err := openFlagged(*path)
	if err != nil {
		fmt.Printf("\nopen failed: %v\n", err)
		return nil
	}
	defer dev.Close()

	fmt.Printf("\nopened        %s\n", dev.Path())
	if size, err := dev.DescriptorSize(); err == nil {
		fmt.Printf("descriptor    %d bytes\n", size)
	} else {
		fmt.Printf("descriptor    unavailable (%v)\n", err)
	}
	return nil
}

// runReportMap prints the reports the device's own HID report descriptor
// declares. It answers a question that a vendor's web tool usually decides
// silently: whether the vendor channel is an output report (sendable by any
// WebHID app) or a feature report (which WebHID tools that send output reports
// cannot use).
func runReportMap(args []string) error {
	fs, path := newFlagSet("reportmap", "List every input, output and feature report the descriptor declares.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	desc, err := dev.Descriptor()
	if err != nil {
		return err
	}
	reports, err := hidraw.ParseReportDescriptor(desc)
	if err != nil {
		return fmt.Errorf("parsing the report descriptor of %s: %w", dev.Path(), err)
	}

	fmt.Printf("device          %s\n", dev.Path())
	fmt.Printf("descriptor      %d bytes\n", len(desc))
	if reports.HasReportIDs {
		fmt.Printf("report ids      explicit (declared with 0x85 items)\n")
	} else {
		fmt.Printf("report ids      none declared; every report uses id 0\n")
	}
	if reports.LongItems > 0 {
		fmt.Printf("long items      %d skipped (they never describe reports)\n", reports.LongItems)
	}

	for _, kind := range []hidraw.ReportKind{hidraw.Input, hidraw.Output, hidraw.Feature} {
		fields := reports.Of(kind)
		if len(fields) == 0 {
			fmt.Printf("%s reports   none\n", kind)
			continue
		}
		fmt.Printf("%s reports\n", kind)
		for _, f := range fields {
			fmt.Printf("  id 0x%02x  %8d bits  %3d bytes\n", f.ReportID, f.Bits, f.Bytes())
		}
	}

	// The distinction that decides vendor-software compatibility, spelled out.
	if _, isInput := reports.Find(hidraw.Input, 8); isInput {
		if field, isOutput := reports.Find(hidraw.Output, 8); isOutput {
			fmt.Printf("\nreport id 8     input + output (%d bytes each): an output-report tool can bind\n", field.Bytes())
		} else {
			fmt.Printf("\nreport id 8     input only, no output report: an output-report tool cannot bind\n")
		}
	}
	return nil
}

// waitFor reads reports until one parses with parse, or the timeout expires.
func waitFor(dev *hidraw.Device, timeout time.Duration, parse func([]byte) bool) ([]byte, bool) {
	buf := make([]byte, hidraw.ReadBufferSize)
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false
		}
		n, err := dev.ReadReport(buf, remaining)
		if err != nil {
			return nil, false
		}
		report := make([]byte, n)
		copy(report, buf[:n])
		if parse(report) {
			return report, true
		}
	}
}

func runBattery(args []string) error {
	fs, path := newFlagSet("battery", "Print the battery level, waiting for the next heartbeat.")
	wait := fs.Duration("wait", 5*time.Second, "how long to wait for a heartbeat")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		_, isBattery := mouse.ParseBattery(r)
		return isBattery
	})
	if !ok {
		return fmt.Errorf("no battery report within %s (is the mouse switched on and paired?)", *wait)
	}

	battery, _ := mouse.ParseBattery(report)
	fmt.Printf("%s\n", battery)
	fmt.Printf("raw           % x\n", report)
	return nil
}

func runStage(args []string) error {
	fs, path := newFlagSet("stage", "Print the current DPI stage.")
	wait := fs.Duration("wait", 5*time.Second, "how long to wait for a stage announcement")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		_, isStage := mouse.ParseStage(r)
		return isStage
	})
	if !ok {
		return fmt.Errorf("no DPI stage announcement within %s.\n"+
			"       The mouse only sends one when the stage changes, so tap its DPI button\n"+
			"       while this runs. Nothing is wrong if this stays quiet.", *wait)
	}

	stage, _ := mouse.ParseStage(report)
	fmt.Printf("%s\n", stage)
	fmt.Printf("raw           % x\n", report)
	fmt.Printf("note          the value behind this stage is not in this frame; \"attackshark-probe dpi\" reads it\n")
	return nil
}

// runDPI reads the DPI configuration the mouse actually stores, using the
// two-step permission handshake the firmware requires. Unlike every other
// subcommand it sends one frame, the 0xA0 mailbox request.
func runDPI(args []string) error {
	fs, path := newFlagSet("dpi", "Read the stored per-stage DPI values from the device.")
	raw := fs.Bool("raw", false, "also hexdump the configuration report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	cfg, err := mouse.ReadDPIConfig(dev)
	if err != nil {
		return fmt.Errorf("reading the DPI configuration from %s: %w\n"+
			"       The device may be on the wrong hidraw collection, or this firmware may\n"+
			"       not implement the 0xA0 mailbox. The tray leaves the stage unlabelled\n"+
			"       when this read fails.", dev.Path(), err)
	}

	fmt.Printf("device        %s\n", dev.Path())
	fmt.Printf("profile       %d\n", cfg.Profile)
	fmt.Printf("active stage  %d of %d\n", cfg.ActiveStage, mouse.MaxStage)
	fmt.Printf("enabled       %s\n", enabledStages(cfg.ActiveMask))
	fmt.Println("stages:")
	for i := 1; i <= mouse.MaxStage; i++ {
		value, _ := cfg.Stages.At(i)
		mark := " "
		if i == cfg.ActiveStage {
			mark = "*"
		}
		fmt.Printf("  %s stage %d  %5d DPI\n", mark, i, value)
	}
	if cfg.ActiveStage >= 1 && cfg.ActiveStage <= mouse.MaxStage {
		fmt.Println("  * = stage the device says is selected; the tray shows the stage the mouse announces")
	}
	if *raw {
		fmt.Printf("\nraw           % x\n", cfg.Raw)
	}
	return nil
}

// runSetStage changes the selected DPI stage. It is the only subcommand that
// changes a setting: the configuration is read first, only the stage byte is
// replaced, and the device's 0x50 confirmation decides whether the switch is
// reported as done.
func runSetStage(args []string) error {
	fs, path := newFlagSet("set-stage", "Switch the selected DPI stage and wait for the device's confirmation.")
	wait := fs.Duration("wait", 3*time.Second, "how long to wait for the confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: attackshark-probe set-stage <1-%d>", mouse.MaxStage)
	}
	stage, err := strconv.Atoi(fs.Arg(0))
	if err != nil || stage < 1 || stage > mouse.MaxStage {
		return fmt.Errorf("stage must be a number from 1 to %d, got %q", mouse.MaxStage, fs.Arg(0))
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	frame, err := mouse.ApplyStage(dev, stage)
	if err != nil {
		return fmt.Errorf("switching to stage %d: %w", stage, err)
	}
	fmt.Printf("wrote         report 0x%02x, %d bytes, selected stage %d\n",
		mouse.DPIReportID, len(frame), stage)

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		conf, isConfirmation := mouse.ParseCommandConfirmation(r)
		return isConfirmation && conf.ReportID == mouse.DPIReportID
	})
	if !ok {
		return fmt.Errorf("no confirmation within %s; the change may not have been applied", *wait)
	}
	conf, _ := mouse.ParseCommandConfirmation(report)
	if !conf.Success {
		return fmt.Errorf("the device rejected the change (status 0x%02x)", conf.Status)
	}
	fmt.Printf("confirmed     report 0x%02x accepted (status 0x%02x)\n", conf.ReportID, conf.Status)

	cfg, err := mouse.ReadDPIConfig(dev)
	if err != nil {
		fmt.Printf("read back     unavailable: %v\n", err)
		return nil
	}
	fmt.Printf("read back     stage %d of %d\n", cfg.ActiveStage, mouse.MaxStage)
	if cfg.ActiveStage != stage {
		return fmt.Errorf("the device reports stage %d after stage %d was written", cfg.ActiveStage, stage)
	}
	return nil
}

// runSetDPI sets one stage's DPI value. It reads the configuration first - both
// to learn which stage the device says is selected and to keep every other
// setting in the report - then writes it back with only that stage's code bytes
// and double-speed flag replaced.
func runSetDPI(args []string) error {
	fs, path := newFlagSet("set-dpi", "Set the DPI value of a stage and wait for the device's confirmation.")
	wait := fs.Duration("wait", 3*time.Second, "how long to wait for the confirmation")
	stageFlag := fs.Int("stage", 0, "stage to change (0 = the stage the device says is selected)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: attackshark-probe set-dpi <1-%d> [-stage 1-%d]", mouse.MaxDPI, mouse.MaxStage)
	}
	value, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("the DPI value must be a number, got %q", fs.Arg(0))
	}
	if value < 1 || value > mouse.MaxDPI {
		return fmt.Errorf("%d DPI is outside 1-%d, the range the %s supports", value, mouse.MaxDPI, mouse.SensorName)
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	cfg, err := mouse.ReadDPIConfig(dev)
	if err != nil {
		return fmt.Errorf("reading the configuration from %s: %w", dev.Path(), err)
	}
	stage := *stageFlag
	if stage == 0 {
		stage = cfg.ActiveStage
	}
	if stage < 1 || stage > mouse.MaxStage {
		return fmt.Errorf("the device reports stage %d, outside 1-%d; pass -stage explicitly", stage, mouse.MaxStage)
	}

	frame, err := mouse.BuildDPIChange(cfg, stage, value)
	if err != nil {
		return fmt.Errorf("building the change to %d DPI on stage %d: %w", value, stage, err)
	}
	if err := dev.SetFeature(frame); err != nil {
		return fmt.Errorf("writing the DPI configuration: %w", err)
	}
	fmt.Printf("wrote         report 0x%02x, %d bytes, stage %d = %d DPI\n",
		mouse.DPIReportID, len(frame), stage, value)

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		conf, isConfirmation := mouse.ParseCommandConfirmation(r)
		return isConfirmation && conf.ReportID == mouse.DPIReportID
	})
	if !ok {
		// As with the tray: silence is checked against the device rather than
		// assumed to be a failure.
		verify, err := mouse.ReadDPIConfig(dev)
		if err == nil {
			got, _ := verify.Stages.At(stage)
			if got == value {
				fmt.Printf("confirmed     no 0x50 reply within %s, but the device reads back %d DPI on stage %d\n", *wait, got, stage)
				return nil
			}
			return fmt.Errorf("the device reports %d DPI on stage %d after %d was written", got, stage, value)
		}
		return fmt.Errorf("no confirmation within %s, and reading the value back failed: %w", *wait, err)
	}
	conf, _ := mouse.ParseCommandConfirmation(report)
	if !conf.Success {
		return fmt.Errorf("the device rejected the change (status 0x%02x)", conf.Status)
	}
	fmt.Printf("confirmed     report 0x%02x accepted (status 0x%02x)\n", conf.ReportID, conf.Status)

	verify, err := mouse.ReadDPIConfig(dev)
	if err != nil {
		fmt.Printf("read back     unavailable: %v\n", err)
		return nil
	}
	got, _ := verify.Stages.At(stage)
	fmt.Printf("read back     stage %d = %d DPI\n", stage, got)
	if got != value {
		return fmt.Errorf("the device reports %d DPI after %d was written", got, value)
	}
	return nil
}

// runPolling reads the polling rate the device is set to. Like "dpi" it sends
// one 0xA0 permission request first, which changes no setting.
func runPolling(args []string) error {
	fs, path := newFlagSet("polling", "Read the polling rate the device is set to.")
	raw := fs.Bool("raw", false, "also hexdump the polling-rate report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	cfg, err := mouse.ReadPollingRate(dev)
	if err != nil {
		return fmt.Errorf("reading the polling rate from %s: %w\n"+
			"       The device may be on the wrong hidraw collection, or this firmware may\n"+
			"       not implement the 0xA0 mailbox.", dev.Path(), err)
	}

	fmt.Printf("device        %s\n", dev.Path())
	fmt.Printf("profile       %d\n", cfg.Profile)
	fmt.Printf("rate          %s (%s)\n", cfg.Rate, cfg.Rate.Name())
	if *raw {
		fmt.Printf("\nraw           % x\n", cfg.Raw)
	}
	return nil
}

// runSetPolling changes the polling rate and waits for the device's own
// confirmation. Unlike "set-stage" no configuration has to be preserved: report
// 0x06 holds the rate and nothing else.
func runSetPolling(args []string) error {
	fs, path := newFlagSet("set-polling", "Switch the polling rate and wait for the device's confirmation.")
	wait := fs.Duration("wait", 3*time.Second, "how long to wait for the confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: attackshark-probe set-polling <125|250|500|1000>")
	}
	hz, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("the polling rate must be a number of Hz, got %q", fs.Arg(0))
	}
	rate := mouse.PollingRate(hz)
	if !rate.Valid() {
		return fmt.Errorf("%d Hz is not a rate this mouse supports (125, 250, 500 or 1000 Hz)", hz)
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	frame, err := mouse.ApplyPollingRate(dev, rate)
	if err != nil {
		return fmt.Errorf("switching to %s: %w", rate, err)
	}
	fmt.Printf("wrote         report 0x%02x, %d bytes, %s for profile %d\n",
		mouse.PollingReportID, len(frame), rate, frame[2])

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		conf, isConfirmation := mouse.ParseCommandConfirmation(r)
		return isConfirmation && conf.ReportID == mouse.PollingReportID
	})
	if !ok {
		// The 0x50 reply is sometimes not delivered even when the write landed,
		// so silence is settled by reading the rate back rather than called a
		// failure. The tray does the same thing.
		verified, err := mouse.ReadPollingRate(dev)
		if err == nil && verified.Rate == rate {
			fmt.Printf("confirmed     no 0x50 reply within %s, but the device reads back %s\n", *wait, verified.Rate)
			return nil
		}
		if err != nil {
			return fmt.Errorf("no confirmation within %s, and reading the rate back failed: %w", *wait, err)
		}
		return fmt.Errorf("the device still reports %s after %s was written", verified.Rate, rate)
	}
	conf, _ := mouse.ParseCommandConfirmation(report)
	if !conf.Success {
		return fmt.Errorf("the device rejected the change (status 0x%02x)", conf.Status)
	}
	fmt.Printf("confirmed     report 0x%02x accepted (status 0x%02x)\n", conf.ReportID, conf.Status)

	cfg, err := mouse.ReadPollingRate(dev)
	if err != nil {
		fmt.Printf("read back     unavailable: %v\n", err)
		return nil
	}
	fmt.Printf("read back     %s\n", cfg.Rate)
	if cfg.Rate != rate {
		return fmt.Errorf("the device reports %s after %s was written", cfg.Rate, rate)
	}
	return nil
}

// enabledStages renders the active-stage bitmask, bit 0 being stage 1.
func enabledStages(mask byte) string {
	var on []string
	for i := 1; i <= mouse.MaxStage; i++ {
		if mask&(1<<(i-1)) != 0 {
			on = append(on, strconv.Itoa(i))
		}
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, ", ")
}

func runStream(args []string) error {
	fs, path := newFlagSet("stream", "Print every status report as it arrives.")
	duration := fs.Duration("duration", 0, "stop after this long (0 runs until interrupted)")
	changesOnly := fs.Bool("changes-only", false, "only print reports that differ from the previous one")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	fmt.Printf("streaming from %s (ctrl-c to stop)\n", dev.Path())
	start := time.Now()
	var lastBattery *mouse.Battery
	var lastStage *mouse.Stage
	buf := make([]byte, hidraw.ReadBufferSize)
	other := 0

	for {
		if *duration > 0 && time.Since(start) >= *duration {
			break
		}
		n, err := dev.ReadReport(buf, time.Second)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return err
		}
		report := buf[:n]
		stamp := time.Since(start).Round(10 * time.Millisecond)

		if battery, ok := mouse.ParseBattery(report); ok {
			if *changesOnly && lastBattery != nil && *lastBattery == battery {
				continue
			}
			lastBattery = &battery
			verb := "battery"
			switch battery.Charge {
			case mouse.ChargeCharging:
				verb = "battery (charging)"
			case mouse.ChargeFull:
				verb = "battery (full)"
			case mouse.ChargeUnknown:
				verb = fmt.Sprintf("battery (charge 0x%02x)", battery.ChargeRaw)
			}
			fmt.Printf("[%8s] %-18s %d %%\n", stamp, verb, battery.Percent)
			continue
		}
		if stage, ok := mouse.ParseStage(report); ok {
			if *changesOnly && lastStage != nil && lastStage.Stage == stage.Stage {
				continue
			}
			lastStage = &stage
			note := ""
			if !stage.Valid() {
				note = "  (outside the expected 1-6 range)"
			}
			fmt.Printf("[%8s] %-18s %s%s\n", stamp, "dpi stage", stage, note)
			continue
		}
		other++
	}

	if other > 0 {
		fmt.Printf("\n%d frames on this channel were neither battery nor stage reports (use dump to see them)\n", other)
	}
	return nil
}

func runDump(args []string) error {
	fs, path := newFlagSet("dump", "Hexdump every report arriving on the configuration channel.")
	duration := fs.Duration("duration", 5*time.Second, "how long to listen")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	fmt.Printf("hexdumping %s for %s\n", dev.Path(), *duration)
	start := time.Now()
	buf := make([]byte, hidraw.ReadBufferSize)
	count := 0

	for {
		remaining := *duration - time.Since(start)
		if remaining <= 0 {
			break
		}
		n, err := dev.ReadReport(buf, remaining)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			return err
		}
		count++
		fmt.Printf("[%8s] len=%d  % x\n",
			time.Since(start).Round(10*time.Millisecond), n, buf[:n])
	}
	fmt.Printf("\n%d reports\n", count)
	return nil
}
