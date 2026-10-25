# Attack Shark X11 on Linux

A tray app for the Attack Shark X11 wireless mouse: battery level and charging
state in the icon, the exact percentage and DPI stage in the tooltip, and the
DPI stage in the menu. Strictly **read-only** — the mouse announces both values
on its own, so nothing here ever writes to the device.

![icon states](docs/icon-states.png)

*The icon is a shark's dorsal fin. Its colour is the battery state — red at 25 %
and below, amber to 50 %, green above — and charging replaces the fin with a
lightning bolt. With no receiver it goes grey. The fin is a Jaws-style
silhouette: a concave leading edge, a convex trailing sweep, and a flat
waterline base, drawn to fill the square so it stays legible at the 22 px a
system tray actually gives it; the exact percentage is in the tooltip.*

Written in Go against `fyne.io/systray`, which speaks the StatusNotifierItem
D-Bus protocol directly. No GTK, no AppIndicator, no `python3-gi` at runtime,
one self-contained binary.

## What it can and cannot show

| | |
|---|---|
| Battery percentage | **Yes**, exact, refreshed every ~2 s, and stamped with its age |
| Charging state | **Yes**, reported by the mouse |
| Current DPI stage (of 6) | **Yes**, but only announced when it *changes* |
| Changing the DPI stage | **No.** This app never writes to the mouse |
| DPI *value* per stage | **Not from the device.** You supply it once — see below |

The last two rows are the honest ones, so they get their own sections.

## DPI values: what the vendor publishes, and what it does not

This is the part that most tools get wrong, so here is exactly what is
established and how.

**Published, from the vendor's own product page for the X11:**

| | |
|---|---|
| Sensor | PixArt PAW3311 |
| Maximum DPI | 22,000 |
| DPI stages | 6, all adjustable |
| Polling rate | 1000 Hz |

**Not published anywhere: the factory DPI value of each individual stage.**
The vendor's specification gives the stage *count* and the sensor *ceiling*, and
nothing more. That was checked against the product pages, the official
driver/firmware/manual download page, general search results and the community
projects listed under [Credits](#credits), and no factory table exists in any of
them.

That matters because the number is genuinely useful to have and genuinely easy
to fake. The mouse reports which of its six stages is *selected*; it never
reports what that stage is *set to*. So a tool that displays "1600 DPI" for this
mouse is either reading the vendor's own configuration or inventing a number —
and on Linux it cannot be reading the configuration, because the reads return
garbage (see [Negative results](#negative-results-what-does-not-work)).

This app therefore treats the value as configuration rather than telemetry:

```bash
# One-off, on the command line:
attackshark-x11-tray -dpi 800,1600,2400,3200,6400,22000

# Or persistently, which is what the autostart entry uses:
$EDITOR ~/.config/attackshark-x11/dpi.conf
```

The file is `stage=value`, one per line, all six required:

```ini
# DPI value of each stage, as shown by the vendor's software.
1=800
2=1600
3=2400
4=3200
5=6400
6=22000
```

`examples/dpi.conf` is an annotated copy of that, and `tools/install.sh` drops
it into place if you do not already have one. The tray's own menu reaches it too:
**Open DPI config** opens the file in your editor, writing that template first if
the file does not exist yet. The values are read once at startup, so restart the
tray to pick up an edit. Values outside 1–22,000 are
rejected, because the sensor's published maximum makes a bigger number a typo
rather than a setting. A malformed file is a startup error rather than something
silently ignored — quietly discarding values you deliberately wrote is worse
than refusing to start.

**Where to read the real numbers: the vendor's Windows software, and only
that.** They live in the mouse, and the tool that reads them for this model is a
Windows application. Attack Shark also runs a browser-based configuration tool,
the "ATTACK SHARK HUB" at `controlhub.top`, but **it does not support this
mouse** — verified, not assumed, and documented in
[docs/vendor-hub.md](docs/vendor-hub.md). Two independent reasons, neither of
them about Linux:

- The hub's device database lists a single vendor ID, `0x3554`. This receiver
  is `1d57:fa60`, so the hub's WebHID filter never offers it.
- The hub binds only to a HID collection exposing exactly one input report and
  one **output** report with report ID 8 — it talks to mice by sending output
  reports. This receiver declares **no output reports at all**, on any
  interface (`attackshark-probe reportmap` prints the full map).

So it would fail identically on Windows in the same Chromium. The vendor's
actual tool for the X11 is `ATTACK SHARK X11 SOFT.exe`, Windows-only, and the
X11 manual is an image-only PDF with the text converted to outlines. Neither is
a source you can read on Linux.

Until you have those numbers, set nothing: the tray shows `DPI: stage 3 of 6`
with no invented value attached, which is the honest output. Once you know them,
they are configuration, exactly as above — and if you have changed a stage, your
own numbers are the only correct ones.

Until you configure them, the tray shows `DPI: stage 3 of 6` with no invented
value attached, and the DPI stages submenu lists all six with the vendor's
sensor facts in the tooltip.

### Why switching is not supported

The mouse's own DPI button is the supported way to change stages, and this app
deliberately does not offer an alternative. The vendor's report descriptor does
declare a Sinowealth-style vendor block, so writing looked plausible, but every
feature read on this dongle returns unaddressable data and the descriptor
declares no output reports at all. Confirming a write protocol would mean
running the vendor's Windows software under Wine and tracing its HID ioctls;
that is a real project of its own, and until someone does it, no write path
belongs in a tool whose whole value is that it never lies to you.

## Install

Needs Go 1.24+ (the version in `go.mod`) and `make`, which wraps the scripts
below rather than replacing them.

```make
make build       # both binaries into bin/, no root, nothing else touched
make check       # gofmt -l, go vet and go test: the same three CI runs
make install     # per-user: ~/.local/bin, autostart entry, docs, dpi.conf template
make package     # dist/attackshark-x11_<version>_<arch>.deb, needs dpkg-deb
make uninstall   # undo the per-user install
make help        # the same list, plus VERSION=, ARCH= and UNINSTALL_ARGS=
```

There are two ways to install, and they are alternatives rather than layers. A
per-user install from source needs one privileged step for device access; the
package does that step for you, for every user on the machine.

### From source, for one user

```bash
# 1. Device access (once). Needs root, changes nothing else on the system.
sudo tools/install-udev.sh

# 2. Build and install for your user: both binaries into ~/.local/bin, an
#    autostart entry, the docs, and a template at ~/.config/attackshark-x11/dpi.conf.
make install

# 3. Check the whole path, including whatever step 1 could have got wrong.
attackshark-probe doctor
```

Then start it now, or log out and back in:

```bash
~/.local/bin/attackshark-x11-tray -v
```

### As a Debian package, for every user

```bash
make package
sudo apt install ./dist/attackshark-x11_*.deb
```

`apt` rather than `dpkg -i`, so `libc6` and `udev` are resolved for you. The
package puts both binaries in `/usr/bin`, the udev rule in
`/usr/lib/udev/rules.d`, its own copy of the docs in
`/usr/share/doc/attackshark-x11`, and an autostart entry in
`/etc/xdg/autostart` that starts the tray for every user at their next login.
**Step 1 above is therefore not needed**: `postinst` loads the rule itself, and
says so if it could not.

Four things about the package are deliberate:

- **It ships no `dpi.conf`.** The per-stage DPI values belong to your mouse, so
  there is no file under `/etc` for anyone to inherit. The menu's "Open DPI
  config" writes the annotated template into the user's own config directory on
  first use, and `/usr/share/doc/attackshark-x11/dpi.conf.example` is the same
  file to copy by hand.
- **The autostart entry is a conffile.** Editing it is legitimate, so an upgrade
  does not overwrite your version. A plain `dpkg -r` therefore *keeps* it, and
  `postrm` says so, because an entry left pointing at a binary that is no longer
  installed is hard to place. `dpkg --purge` removes it.
- **The version comes from the `VERSION` file.** It holds the version the next
  release is built as, and `make package` reads it. A release is a `v*` tag
  matching that file (`v0.1.0` for `0.1.0`), which builds `amd64` and `arm64`
  packages and attaches both to the GitHub release. `make package VERSION=1.2.3`
  overrides the file for a one-off build; with no file it falls back to
  `git describe --tags --always`, rewritten into something dpkg accepts.
- **Nothing in it writes to the mouse**, exactly as the binaries it contains.

### Undoing either install

```bash
tools/uninstall.sh                   # binaries, autostart entry, installed docs
tools/uninstall.sh --purge           # also dpi.conf and the remembered stage
sudo tools/uninstall.sh --with-udev  # also the udev rule in /etc
sudo tools/uninstall.sh --with-package   # also a system-wide .deb install
```

`make uninstall` runs that script, and `make uninstall UNINSTALL_ARGS=--purge`
passes the flag through. The two root-only flags are checked before anything is
removed, so forgetting `sudo` fails with a message instead of leaving a
half-removed installation, and the script removes the *invoking* user's files
when run through `sudo` rather than looking in root's home directory.

Your `dpi.conf` and the remembered stage survive a plain `uninstall.sh` on
purpose: they describe your mouse, and nothing can regenerate them.

`doctor` is the quickest way to tell an installation problem from a mouse that is
simply asleep: it prints one line per check and exits non-zero only when
something actually stops the app from working. It recognises both installs, so
either `/usr/bin/attackshark-x11-tray` from the package or
`~/.local/bin/attackshark-x11-tray` from the source install counts as present.

### The udev rule, and why it looks the way it does

`/dev/hidraw*` is `crw------- root root` by default, so the app needs an
exception. Two things about `udev/99-attackshark-x11.rules` are deliberate:

- **It is split into two rules.** Every `ATTRS` match within one rule must
  resolve to the same parent device, and udev fails *silently* otherwise. The
  obvious single rule combining `ATTRS{idVendor}` (on the USB device) with
  `ATTRS{bInterfaceNumber}` (on the USB interface) matched nothing at all: no
  ACL, no symlink, no error message. Rule 2 avoids the problem by matching the
  interface's `modalias`, which carries vendor, product and interface number in
  a single attribute.
- **It grants access via `GROUP="plugdev"` rather than relying on
  `TAG+="uaccess"`.** Measured on Linux Mint 22.3 / Cinnamon, the `uaccess` tag
  *is* applied to all four of the receiver's hidraw nodes, but no ACL ever
  appears, because no device in the receiver's parent chain gets `ID_SEAT`, so
  logind has nothing to grant it for. (`/dev/uinput` and `/dev/snd/controlC0`
  do get a user ACL, which is how the difference was pinned down.) The
  `uaccess` tag is kept for distributions where it works; `plugdev` is the
  Debian/Ubuntu convention and is what actually grants access here. Members of
  other distributions should either join the user to `plugdev` or change
  `GROUP=` to a group they are in.
- **It covers all four interfaces, unavoidably.** They belong to one USB
  device, so the device-level rule cannot target only interface #2. Those other
  channels are already reachable through `/dev/input`, and Valve's own
  `60-steam-input.rules` on this machine uses the same device-level pattern.

Rule 2 also creates the stable symlink `/dev/attackshark-x11`. The hidraw node
number is not stable — this receiver has been seen as `hidraw2` and as
`hidraw3` — so the app prefers the symlink and falls back to scanning sysfs by
USB ID if it is missing.

## Usage

### Tray app

```bash
attackshark-x11-tray -h
```

| flag | meaning |
|---|---|
| `-icon-size` | tray icon edge length, default 24 px |
| `-dpi` | the six per-stage DPI values, in stage order |
| `-dpi-config` | file to read them from when `-dpi` is not given |
| `-poll-timeout` | how long a single device read may block |
| `-retry-every` | delay before re-opening the receiver after it disappears |
| `-state-dir` | where the last observed stage is remembered (default `~/.local/state/attackshark-x11`) |
| `-no-notify` | suppress desktop notifications |
| `-v` | log device activity to stderr |

The menu shows the live battery reading, the DPI stage with its configured
value, the connection state, and a read-only **DPI stages** submenu listing all
six stages with the current one ticked. **Open DPI config** opens the file the
values come from in your own editor, creating it from the annotated template if
it is missing. On quit, replacing the receiver, or
unplugging it, the icon turns grey and the app reopens the device when it
returns — no restart needed.

**One instance per session.** A second copy exits with a message naming the lock
file instead of adding a second icon, because two instances would both open the
receiver and compete for its reports. The lock is a kernel lock on an open file
descriptor, so nothing stale can survive a crash or a `kill -9`.

**A reading that stops arriving says so.** Every battery report is timestamped,
and the age is shown once the heartbeats stop: the tooltip reads
`78 % (discharging)` while reports are flowing, `78 % (discharging), last read
12 s ago` after a few missed heartbeats, and
`no reports for 12 min; last read 78 % (discharging)` when the mouse has gone
quiet — switched off, asleep or out of range. The menu's battery line and
connection line carry the same age, and `-v` logs the transition once per quiet
spell:

```
[attackshark] no battery reports for 1 min 0 s; last read 78 % (discharging)
[attackshark] battery reports resumed after 3 min 12 s
```

This exists because the mouse's own behaviour forced the question. Its
percentage sat at exactly `78 %` for more than a day, and there was no way to
tell a live reading from a frozen one:

```
Sep 17 23:42   03 55 40 01 4e   78 %  discharging   (every frame, 24 h)
Sep 18 22:10   03 55 40 03 4e   78 %  charging      (flipped 2 s after plugging in)
Sep 18 23:10   03 55 40 01 55   85 %  discharging   (after charging)
```

The charge-state byte moved within one heartbeat of the cable going in, and the
percentage moved with it — `78 → 87` while charging, then stepping down `1 %` at
a time on battery. So the channel and the gauge are both live, and the gauge is
fine-grained; the mouse simply uses under 1 % of its charge in a day of mostly
idle use, which is why the reading looked frozen. The age is what lets you tell
"nothing is draining" apart from "nothing is arriving". The thresholds are tied
to the observed 2 s heartbeat and a test asserts they stay that way.

**The stage is remembered, not measured.** The mouse announces its stage only
when the stage changes, so on startup nothing is known and the menu reads
`DPI: unknown`. Press the mouse's DPI button once and it syncs. The last
observed stage is written to `~/.local/state/attackshark-x11/stage` and shown
afterwards as `stage N (last seen)` — explicitly marked as remembered rather
than observed, so a stale value cannot masquerade as live telemetry.

### probe tool

```bash
attackshark-probe info       # discovery, interface numbers, descriptor size
attackshark-probe reportmap  # every input/output/feature report the descriptor declares
attackshark-probe battery    # wait for the next heartbeat, print it
attackshark-probe stage      # wait for a stage announcement (tap the DPI button)
attackshark-probe stream     # decode every status report as it arrives
attackshark-probe dump       # hexdump every frame, for reverse engineering
attackshark-probe doctor     # check the installation, not the device
```

Every subcommand is read-only. `-changes-only` on `stream` filters out repeat
heartbeats. `stage` prints the stage number, and says so if you were expecting a
DPI value to come with it.

`doctor` is the one to reach for when the tray shows nothing, or shows less than
you expect. It checks the udev rule and its symlink, opens the receiver the way
the app does and waits for one report, reads your `dpi.conf`, looks for the
installed tray binary and autostart entry, and asks the session bus whether
notifications have a daemon to reach. Each line is `ok`, `warn` or `FAIL`: a
warning is something the app copes with (a missing symlink, a mouse that is
asleep), a failure is something that stops it, and only failures make the command
exit non-zero.

## The protocol

Reverse engineered on real hardware: Attack Shark X11, 2.4 GHz receiver,
**USB `1d57:fa60`** (the receiver identifies itself as `Xenta 2.4G Wireless
Device`). It is a four-interface composite HID device; only **USB interface #2**
carries the vendor channel described here.

The channel pushes **HID report ID `0x03`, five bytes**, with two subtypes:

```
03 55 40 01 4e     battery:  byte 3 = charge state, byte 4 = percent
                   (0x01 discharging, 0x03 charging, 0x4e = 78 %)
                   pushed every ~2.00 s while the mouse is awake

03 55 10 03 00     DPI stage: byte 3 = current stage, 1..6
                   pushed only when the stage changes
```

Frames are `03 55 <kind> <value> 00` except for the battery subtype, whose
final byte is the percentage. Both subtypes share report ID `0x03`, so they are
distinguished by byte 2: `0x40` battery, `0x10` stage.

**Six stages, confirmed by observation.** Six consecutive presses of the
mouse's DPI button produced a clean wraparound, captured live from the device:

```
t= 5.72   03 55 10 04 00
t= 6.63   03 55 10 05 00
t= 7.60   03 55 10 06 00
t= 8.56   03 55 10 01 00
t= 9.91   03 55 10 02 00
t=11.45   03 55 10 03 00
```

Nothing has to be requested for any of this: the receiver volunteers both
values, which is why the display needs no writes and no polling, and why the app
runs unprivileged once the udev rule is in place.

`internal/mouse` is the whole decoder, and it is deliberately strict — it
requires report ID `0x03`, the `0x55` magic and an exact length, and passes
through an out-of-range stage instead of silently dropping it.

## Negative results: what does *not* work

Documented because each of these looks like it should work, and rediscovering
that it does not is expensive.

### Feature reports return unaddressable data

The vendor interface's report descriptor declares a Sinowealth-style feature
report block on Usage Page `0x0B`, with report IDs `0x04`…`0x2E` and lengths
55 / 14 / 8 / 7 / 58 / 63 / 9 / 7 / 133 / 132 … — the signature of the protocol
used by most of these 2.4 GHz gaming mice, and the reason reading the DPI
configuration looked promising.

But `HIDIOCGFEATURE` on this dongle **does not honour the requested report ID**.
Reading every declared ID returned the same blob, and a control read of report
ID `0x3f` — which is *not declared in the descriptor at all* — succeeded just as
happily. The longer responses even contained recycled mouse-motion and battery
bytes. A repeat run that read each report three times and kept only bytes that
agreed across all three reads confirmed it: some bytes never agreed at all,
others stalled, and everything else changed wholesale once and then froze. That
is kernel buffer residue, not device configuration.

Bypassing hidraw with raw usbfs control transfers reaches the same conclusion
differently: they fail with `EBUSY`, because `usbfs` refuses control transfers
to an interface the `usbhid` driver owns, so a clean `GET_REPORT` would require
detaching the interface from the kernel.

**Consequence: DPI values per stage are unreadable from Linux by any method
tried, and the app does not pretend otherwise.**

### The descriptor declares no output reports

Read from the hardware, the configuration interface's 230-byte report
descriptor declares input reports with IDs `0x01`, `0x02` and `0x03`, **no
output reports at all**, and 24 feature reports (`0x04`…`0x2E`, plus `0xA0`)
with lengths from 6 to 133 bytes. `attackshark-probe reportmap` prints exactly
that, item by item, straight off the device.

So there is no documented channel for a stage change even if one wanted to write
one. That is the second reason this app has no switching: not only is the write
protocol unknown, the descriptor offers no output report to carry it.

It is also why the vendor's own browser tool cannot configure this mouse. That
tool sends commands as an output report — it requires a collection with one
input report and one output report with report ID 8 — so a device with no output
reports is invisible to it. Notably, report ID 8 *does* exist here, as a 58-byte
**feature** report, which is a different HID direction and unreachable by
`sendReport`. [docs/vendor-hub.md](docs/vendor-hub.md) has the full
demonstration, including the hub's device database and the binding check.

## Design notes

- **One goroutine owns the file descriptor.** Reads and reconnects happen on it;
  the UI is updated from there, so nothing races a device read.
- **The icon is generated, not shipped.** `internal/batteryicon` draws the fin
  with `image`/`image/color`/`image/png` from the standard library as a polygon
  outline, filled by the even-odd rule at 4× and box-filtered for antialiasing,
  averaged in premultiplied space so edges do not pick up dark halos against
  transparency. The bolt is a zigzag stroked as quads with a disc at each joint,
  because at 22 px a filled bolt's waist pinches to nothing and reads as a
  sparkle. `State.Key()` makes regeneration conditional: the mouse reports every
  2 s, but a PNG is only produced when the rendered appearance actually changes —
  a handful per day, not 43,000.
- **The glyph answers two questions, not three.** Colour says roughly how much
  is left, the bolt says whether it is plugged in, and the tooltip carries the
  number. Earlier revisions also drew the exact level inside the shape, but at
  22 px a silhouette that is both recognisable and a gauge is fighting itself:
  every extra mechanism cost the fin legibility. `attackshark-icons -html` writes
  a self-contained page showing every state on a light and a dark panel, which is
  the only way to judge that trade-off by eye.
- **Notifications are fire-and-forget, not fire-once.** Low battery warns at
  20 % and 10 %, once per threshold per discharge cycle, re-armed when charging
  starts. "Full" is a deliberate heuristic (≥99 % while charging) and is
  commented as such, because the mouse has no distinct full state. A post that
  fails is retried once after five minutes, and the notifier refuses to try again
  for that long in between: a desktop whose notification daemon is missing, or has
  just been restarted, loses one warning rather than every notification for the
  rest of the session.
- **No package in this repository writes to the device.** The only ioctls
  implemented are reads; `GetFeature` is kept solely to document the negative
  result above.
- **A wrong label is worse than no label.** That principle is what made the
  DPI values a config file rather than a lookup table, and the tests assert that
  an unconfigured mapping never produces a number in the tooltip.

## Credits

Two community projects made this much faster to write, and both are worth
looking at:

- [daviirodrig/attackshark](https://github.com/daviirodrig/attackshark) —
  independently targets the same `1d57:fa60` receiver and decodes the battery
  frame the same way (`report[3]` charge, `report[4]` percent). Arriving at the
  same layout separately is the strongest confirmation of the battery half of
  this protocol.
- [mdmassa/attackshark-linux-setup](https://github.com/mdmassa/attackshark-linux-setup) —
  the Windows-driver setup and udev scaffolding that established this class of
  device is drivable from Linux at all.

The vendor's own product page is the source for the sensor, the 22,000 DPI
maximum and the six stages; it is also the source for the absence of a factory
per-stage table, since nothing on it or on the official download page lists one.

Testing the raw channel is much easier with `usbhid-dump` (from
`usbutils`/`hid-tools`) and `evtest`.

## Development

```bash
go test ./...     # protocol, DPI parsing, config loading, icon geometry, descriptor parser
go vet ./...
gofmt -l .
```

`make check` runs those three in that order, and fails on unformatted code rather
than printing a list to ignore. The `Makefile` is a wrapper: `make install` and
`make uninstall` call `tools/install.sh` and `tools/uninstall.sh` so that the
per-user layout is described in one place, and only `make package` has a layout
of its own, because a package installs to `/usr` instead of a home directory.

`.github/workflows/ci.yml` runs those three on every push and pull request, plus
a shell syntax check and a ten-second run of each fuzz target.
`.github/workflows/release.yml` runs on a `v*` tag: it checks that the tag names
the version in `VERSION`, then builds the `amd64` and `arm64` packages and
attaches both to the GitHub release. The parsers are
fuzzed as well as unit tested, because a byte stream from the mouse and a config
file written by hand are both untrusted input:

```bash
go test ./internal/mouse -run='^$' -fuzz=FuzzParseBattery -fuzztime=30s
go test ./internal/mouse -run='^$' -fuzz=FuzzParseDPIStages -fuzztime=30s
go test ./internal/trayapp -run='^$' -fuzz=FuzzLoadDPIStages -fuzztime=30s
```

A failing input is written to `testdata/fuzz/<target>/` and is a permanent case
from then on; the seed corpus in each test holds the frames and files captured
from real hardware.

The icon can be rendered without a running tray, which is how the image at the
top of this file is regenerated:

```bash
go run ./cmd/attackshark-icons -out docs/icon-states.png    # the strip above

go run ./cmd/attackshark-icons -size 22 -scale 8 -html /tmp/icons.html
                                                           # standalone page, both panels
```
