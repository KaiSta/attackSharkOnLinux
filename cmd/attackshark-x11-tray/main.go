// Command attackshark-x11-tray shows the Attack Shark X11's battery level and
// DPI stage in the system tray.
//
// The icon is a shark's dorsal fin whose colour follows the charge level (red
// below 25%, amber up to 50%, green above), replaced by a lightning bolt while
// charging. The tooltip carries the precise percentage, and the menu shows the
// DPI stage and what each of the six stages is set to.
//
// The per-stage DPI values, the selected stage and the polling rate are read
// from the mouse's own configuration when it is opened, each through the
// firmware's 0xA0 permission request. Picking a stage from the menu writes that
// configuration back with only the selected stage changed, picking a DPI value
// writes it back with that stage's value changed, and picking a rate writes the
// polling-rate report; those are the only three settings this app modifies.
//
// Reading needs no privileges once udev/99-attackshark-x11.rules is installed.
package main

import (
	"flag"
	"fmt"
	"os"

	"attackshark-x11/internal/trayapp"
)

func main() {
	opt := trayapp.DefaultOptions()

	flag.IntVar(&opt.IconSize, "icon-size", opt.IconSize,
		"tray icon edge length in pixels")
	flag.BoolVar(&opt.ReadDPI, "read-dpi", opt.ReadDPI,
		"read the per-stage DPI values, the selected stage and the polling rate from the mouse when it is opened")
	flag.DurationVar(&opt.PollTimeout, "poll-timeout", opt.PollTimeout,
		"how long a single device read may wait")
	flag.DurationVar(&opt.RetryEvery, "retry-every", opt.RetryEvery,
		"how long to wait before trying to open the receiver again")
	flag.BoolVar(&opt.NoNotify, "no-notify", opt.NoNotify,
		"do not post desktop notifications about the battery")
	flag.BoolVar(&opt.Verbose, "v", opt.Verbose,
		"log device activity to stderr")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"attackshark-x11-tray shows the Attack Shark X11's battery level and DPI stage\n"+
				"in the system tray.\n\n"+
				"usage: %s [flags]\n\nflags:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\n"+
			"The DPI value behind each stage, the stage the mouse reports and the polling\n"+
			"rate it reports at are read from the mouse's own configuration when it is\n"+
			"opened (with -read-dpi). The firmware only answers those reads after a one-shot\n"+
			"permission request each.\n\n"+
			"Picking a stage from the DPI stages menu writes the configuration back with\n"+
			"only the selected stage changed, picking a DPI value writes it back with that\n"+
			"stage's value changed, and picking a rate writes the polling-rate report; those\n"+
			"are the only three settings this app modifies.\n\n"+
			"Nothing is remembered between runs: if the read fails, the stage and its value\n"+
			"stay unlabelled until the mouse reports them.\n")
	}
	flag.Parse()

	if err := trayapp.Run(opt); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
