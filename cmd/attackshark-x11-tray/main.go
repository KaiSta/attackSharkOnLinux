// Command attackshark-x11-tray shows the Attack Shark X11's battery level and
// DPI stage in the system tray.
//
// The icon is a shark's dorsal fin whose colour follows the charge level (red
// below 25%, amber up to 50%, green above), replaced by a lightning bolt while
// charging. The tooltip carries the precise percentage, and the menu shows the
// DPI stage and what each of the six stages is set to.
//
// Nothing here writes to the mouse. Reading needs no privileges once
// udev/99-attackshark-x11.rules is installed.
package main

import (
	"flag"
	"fmt"
	"os"

	"attackshark-x11/internal/mouse"
	"attackshark-x11/internal/trayapp"
)

func main() {
	opt := trayapp.DefaultOptions()

	var dpi string
	var dpiConfig string

	flag.IntVar(&opt.IconSize, "icon-size", opt.IconSize,
		"tray icon edge length in pixels")
	flag.StringVar(&dpi, "dpi", "",
		"DPI value of each of the six stages, in stage order, e.g. 800,1600,2400,3200,6400,22000")
	flag.StringVar(&dpiConfig, "dpi-config", opt.DPIConfigPath,
		"file to read the per-stage DPI values from when -dpi is not given")
	flag.DurationVar(&opt.PollTimeout, "poll-timeout", opt.PollTimeout,
		"how long a single device read may wait")
	flag.DurationVar(&opt.RetryEvery, "retry-every", opt.RetryEvery,
		"how long to wait before trying to open the receiver again")
	flag.StringVar(&opt.StateDir, "state-dir", opt.StateDir,
		"where to remember the last observed DPI stage (empty string disables it)")
	flag.BoolVar(&opt.NoNotify, "no-notify", opt.NoNotify,
		"do not post desktop notifications on low battery")
	flag.BoolVar(&opt.Verbose, "v", opt.Verbose,
		"log device activity to stderr")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"attackshark-x11-tray shows the Attack Shark X11's battery level and DPI stage\n"+
				"in the system tray. It only ever reads from the mouse.\n\n"+
				"usage: %s [flags]\n\nflags:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\n"+
			"DPI values are yours to supply. The mouse reports which of its %d stages is\n"+
			"selected, but never the DPI value behind it, and the vendor publishes only\n"+
			"the stage count and the %s sensor's %d DPI maximum - no factory table.\n"+
			"Set the six values with -dpi or in %s (see that\n"+
			"file's format in the README), and the stage will be labelled with them.\n",
			mouse.MaxStage, mouse.SensorName, mouse.MaxDPI, opt.DPIConfigPath)
	}
	flag.Parse()

	if dpi != "" {
		stages, err := mouse.ParseDPIStages(dpi)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: -dpi: %v\n", err)
			os.Exit(2)
		}
		opt.DPI = stages
	}
	opt.DPIConfigPath = dpiConfig

	if err := trayapp.Run(opt); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
