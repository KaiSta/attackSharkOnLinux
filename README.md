# Attack Shark X11 on Linux

A tray app for the Attack Shark X11 wireless mouse: battery level and charging
state in the icon, the exact percentage, DPI stage and polling rate in the
tooltip, and menus that switch DPI stages, set a stage's DPI value and switch
the polling rate. Battery and stage come from what the mouse reports, the
per-stage values and the polling rate are read from it, and the only settings the
app writes are the selected stage, the DPI value of the stage the mouse is on,
and the polling rate — when you pick one. See
[DPI values](#dpi-values-what-the-device-holds-and-how-this-app-reads-it),
[switching stages](#switching-stages-from-the-menu) and
[the polling rate](#polling-rate).

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
| Charging state | **Yes**, reported by the mouse, including a completed charge |
| Current DPI stage (of 6) | **Yes**, read when the device opens and announced when it *changes* |
| Changing the DPI stage | **Yes**, from the **DPI stages** menu, confirmed by the mouse |
| DPI *value* per stage | **Yes**, read from the device when it opens and after every confirmed write |
| Setting a stage's DPI value | **Yes**, from the **DPI value** menu in 2000 DPI steps, confirmed by the mouse |
| Polling rate | **Yes**, read when the device opens; there is no announcement for it |
| Changing the polling rate | **Yes**, from the **Polling rate** menu, confirmed by the mouse |
| Pointer speed | **Yes** on Cinnamon and GNOME, from the **Pointer speed** menu — the desktop's setting, not the mouse's |

The stage and polling rows carry caveats, so they get their own sections.

## DPI values: what the device holds, and how this app reads it

**Published, from the vendor's own product page for the X11:**

| | |
|---|---|
| Sensor | PixArt PAW3311 |
| Maximum DPI | 22,000 |
| DPI stages | 6, all adjustable |
| Polling rate | 1000 Hz |

What the vendor does not publish is the factory value of each stage, and the
mouse does not push it on its status channel either. It is stored in the mouse
and readable over the vendor feature reports — but only after a two-step
handshake, which is why earlier versions of this app concluded it could not be
read at all.

**The handshake.** Report `0xA0` is a one-shot read-permission mailbox. The host
asks with an 8-byte feature `SET_REPORT`:

```
a0 04 38 00 01 00 00 00     read report 0x04 (56 bytes), profile 1
```

then reads `0xA0` back after ~250 ms (byte 1 is `0x01` when a read is permitted)
and reads report `0x04`. The permission covers exactly one read. A feature read
made *without* it returns leftover bytes that look random, which is what made
the channel look unreadable in the first place — see
[Negative results](#negative-results-what-does-not-work).

On the wire that is a HID `SET_REPORT` control transfer — `bmRequestType 0x21`,
`bRequest 0x09`, `wValue 0x0300 | report ID`, `wIndex 2` for the vendor
interface. Linux's hidraw sends it as `HIDIOCSFEATURE` and reads the replies with
`HIDIOCGFEATURE`. No output report is involved, which is why the descriptor
declaring none never actually blocked this.

Report `0x04` carries the six stage codes, the active stage, the enabled-stage
bitmask and a 16-bit checksum over bytes 3–49, which this app verifies before it
believes anything. The codes are the PAW3311's own scale rather than plain
integers, so they are decoded with the sensor's table.

This was implemented from the protocol notes of the
[attack-shark-x11-driver](https://github.com/HarukaYamamoto0/attack-shark-x11-driver)
project, which reverse engineered the same `1d57:fa60` receiver. It is confirmed
on one X11 receiver here — the read returns `800/1600/2400/3200/5000/22000` with
a valid checksum — but it depends on firmware behaviour, and firmware may
differ between units.

### When the read fails

The tray asks for the DPI configuration and the polling rate once per device open
(`-read-dpi`, on by default). A refused permission is retried after a pause,
because this firmware needs a quiet moment between reads: on hardware,
back-to-back reads without that pause failed about half the time. If anything is
off — an older firmware without the mailbox, the wrong hidraw collection, a
mouse that is asleep, a checksum that does not add up, a DPI code this build does
not know — the read is abandoned and nothing is invented:

- the **DPI stages** entries stay as `Stage 3`, and the tooltip says the value
  has not been read;
- the stage row reads `DPI: unknown` until the mouse announces a stage change;
- the polling menu reads `Polling rate: unknown`.

There is no config file and no flag to fill in the numbers. An earlier version
kept a hand-written `dpi.conf` for exactly this case, and it was removed: a value
typed once goes stale the moment the mouse is switched on another machine, and a
stale number shown in the same font as a measured one is worse than a blank.
`attackshark-probe dpi` says why a read failed, which is the thing worth acting
on.

Attack Shark also runs a browser-based configuration tool, the "ATTACK SHARK
HUB" at `controlhub.top`, but **it does not support this mouse** — verified, not
assumed, and documented in [docs/vendor-hub.md](docs/vendor-hub.md). The hub
binds only to a collection with one input and one **output** report with report
ID 8; this receiver declares no output reports at all, and the hub's device
database does not list `1d57:fa60` either. It would fail identically on Windows
in the same Chromium.

### Switching stages from the menu

Click a stage in **DPI stages** to switch the mouse to it. The report carries the
whole configuration, not just a stage number, so the app does a
read-modify-write:

1. read report `0x04` through the `0xA0` mailbox (above);
2. replace byte 24, the selected stage, and recompute the checksum at 50–51;
3. write the 56-byte report back with feature `SET_REPORT`;
4. wait for the device's `0x50` confirmation naming report `0x04`.

The tick follows confirmation, not the click. A rejection (`03 55 50 01 04`),
or a timeout that a read-back of the configuration cannot explain, leaves the
displayed stage alone and posts a notification saying so. The `0x50` reply is
sometimes not delivered even when the write landed, so a timeout is settled by
reading the configuration back and comparing the stage. A `0x10` announcement of
the requested stage counts as confirmation too.

Only the stage changes: the six DPI values, per-stage colours, angle snap,
ripple control and profile are written back exactly as they were read. If the
configuration cannot be read, the app refuses to write, because synthesising one
would silently reset those settings. Switching therefore works even with
`-read-dpi=false`; that flag only controls the startup read the labels come from.

This is the same feature `SET_REPORT` path as the read — `HIDIOCSFEATURE`,
`wValue 0x0304` on the vendor interface, no output report — and it works on the
2.4 GHz receiver (`1d57:fa60`), the only mode this app opens.

The read and the write both address profile 1, which is the profile this unit
reports as active. The X11 has five profiles and this app does not manage them,
so if you have switched the mouse to another profile by its own controls, a
stage picked here may not be the one you see.

### Setting the DPI value from the menu

**DPI value** sets how sensitive the stage the mouse is on is: 2000 DPI up to
22000, in 2000 DPI steps. Whichever entry you click is applied to the stage the
mouse is on at that moment — the other five keep their values — and the tick, the
`DPI:` line and the stage labels follow the device's confirmation.

It is the same read-modify-write as a stage switch, with two code bytes and a
double-speed flag replaced instead of the selected stage:

1. read report `0x04` through the `0xA0` mailbox (above);
2. look up the sensor code for the chosen value;
3. replace bytes 8–13 / 16–21 for that stage's two code bytes and set or clear
   its bit in byte 6 (the flag that doubles a code, which is how 22000 is
   stored), then recompute the checksum at 50–51;
4. write the 56-byte report back and wait for the `0x50` confirmation.

The encoding is not a plain integer: it is the PAW3311's own table, and the app
verifies every code by decoding it again before writing, so a value can never be
written as a code that reads back differently. Setting a value also replaces all
six labels with the configuration just read, because that read is where they came
from.

A stage whose value is not one of the steps (3200, say) simply leaves every box
empty; the `DPI:` line still names it. If the mouse has not reported which stage
it is on, a pick is refused with a notification rather than applied to a guess.

### Polling rate

**Polling rate** lists the four rates the mouse supports, with the current one
ticked. There is no button and no announcement for this setting the way there is
for the stage: the rate is read once when the device opens, and after a change
the tick follows the device's own confirmation.

The report is nine bytes instead of fifty-six:

1. read report `0x06` through the same `0xA0` mailbox;
2. replace the rate code, keeping the profile the report named;
3. write it back with feature `SET_REPORT`, the checksum `0xFF - code` at byte 4;
4. wait for the `0x50` confirmation naming report `0x06`.

| Menu entry | Code | The vendor's name for it |
|---|---|---|
| 125 Hz | `0x08` | power saving |
| 250 Hz | `0x04` | office |
| 500 Hz | `0x02` | gaming |
| 1000 Hz | `0x01` | eSports |

Because the report carries only the rate, a change does not have to preserve
anything: if the read before the write is refused, the write still goes ahead for
profile 1, the profile the DPI code addresses. A missing confirmation is settled
the same way as for a stage, by reading the rate back and comparing it. With
`-read-dpi=false` the menu shows `Polling rate: unknown` until a change is
confirmed, but switching still works.

This unit reads 1000 Hz; the tooltip then ends with `- 1000 Hz polling`.

### Pointer speed

22000 DPI is only useful if the cursor is not unusable with it, so the menu has a
**Pointer speed** submenu. It changes the desktop's own pointer speed — the same
value the Mouse panel's speed slider writes — which the system applies to every
pointing device through libinput, and which is remembered across logins.

- `-1.00` is the slowest the desktop allows, `1.00` the fastest, `0.00` the
  default; the menu offers 0.25 steps.
- The value is read when the app starts, so the tick describes the desktop. A
  value read between two steps (0.30, say) leaves every box empty and is still
  named: `Pointer speed: 0.30`.
- A click writes it with `gsettings` and then reads it back. The tick follows
  the read-back, and a write that cannot be read back shows `Pointer speed:
  unknown` rather than claiming the value that was asked for.
- Like the polling rate, this setting has no notification of its own: a change
  made in the desktop's Mouse panel afterwards is only noticed at the next
  start of the tray.
- It needs the Cinnamon or GNOME mouse settings schema. On another desktop the
  entry is disabled and says so, because there is no portable way to set this.

This is not the mouse's DPI, and it is not a mouse setting at all: the sensor
decides how far the mouse travels per inch, while this decides how far the cursor
moves for that movement. It works with the receiver unplugged, and it is shared
with every other pointing device on the machine.

## Install

One step: build the package and install it. That needs Go 1.24+ (the version in
`go.mod`), `make`, and `dpkg-deb`.

```make
make build       # both binaries into bin/, no root, nothing else touched
make check       # gofmt -l, go vet and go test: the same three CI runs
make package     # dist/attackshark-x11_<version>_<arch>.deb, needs dpkg-deb
make help        # the same list, plus VERSION=, ARCH= and other variables
```

### The package

```bash
make package
sudo apt install ./dist/attackshark-x11_*.deb
```

`apt` rather than `dpkg -i`, so `libc6` and `udev` are resolved for you. The
package puts both binaries in `/usr/bin`, the udev rule in
`/usr/lib/udev/rules.d`, and the docs in `/usr/share/doc/attackshark-x11`;
`postinst` reloads udev, so device access works straight away without a reboot
or a second command.

Nothing is started for you. Check the path and start it:

```bash
attackshark-probe doctor
attackshark-x11-tray -v
```

### If the tray cannot open the receiver

`attackshark-probe doctor` names the check that failed. The usual cause is that
your user is not in the `plugdev` group the rule grants access to, and fixing
the group is something no package can do on your behalf:

```bash
sudo tools/install-udev.sh
```

It installs the same rule into `/etc/udev/rules.d` (where it takes precedence
over the packaged copy), reloads udev, reports whether `/dev/attackshark-x11`
appeared, and tells you when your user needs adding to `plugdev` (then log out
and back in). It is also what a source install uses instead of the package.

### Starting it with the session is your call

**Neither the package nor `tools/install.sh` writes a startup entry.** A tray
icon that appears because a package decided it should is a surprise, and the
right way to start one differs by desktop, so that choice is left to you:

- **Desktop startup applications** — add `/usr/bin/attackshark-x11-tray` (or
  `~/.local/bin/attackshark-x11-tray` for a source install) to the list of
  applications your session starts.
- **systemd user unit** — copy
  [systemd/attackshark-x11-tray.service](systemd/attackshark-x11-tray.service)
  into `~/.config/systemd/user`, then `systemctl --user enable --now
  attackshark-x11-tray.service`.

Either route is fine; enable both and the single-instance lock makes the loser
exit, but a restarting unit next to a working icon is just noise.

Four things about the package are deliberate:

- **It ships no configuration file.** Nothing but the mouse supplies the
  per-stage DPI values, so there is nothing for a new user to inherit or edit.
- **It installs no autostart entry**, for the reason above: starting the tray is
  the user's decision, not the package's.
- **The version comes from the `VERSION` file.** It holds the version the next
  release is built as, and `make package` reads it. A release is a `v*` tag
  matching that file (`v0.1.0` for `0.1.0`), which builds `amd64` and `arm64`
  packages and attaches both to the GitHub release. `make package VERSION=1.2.3`
  overrides the file for a one-off build; with no file it falls back to
  `git describe --tags --always`, rewritten into something dpkg accepts.
- **It changes three mouse settings, and only on request.** The frames it can
  send are the `0xA0` read-permission request, the configuration report with its
  stage byte replaced when a stage is picked from the menu (or one stage's DPI
  codes when a value is picked), and the nine-byte polling-rate report when a
  rate is picked. Nothing else is written, and the binaries it contains behave
  the same way.

### From a source checkout, without a package

```bash
make install          # both binaries into ~/.local/bin, plus the docs
make uninstall        # undo that
```

The install itself needs no root, and writes no startup entry. Device access is
the one privileged step, because nothing here carries the rule for you:
`sudo tools/install-udev.sh` once, as described above. This is an alternative to
the package rather than a layer on top of it: install both and `~/.local/bin`
shadows `/usr/bin` on most `PATH`s.

### Undoing either install

```bash
tools/uninstall.sh                       # per-user binaries and installed docs
sudo tools/uninstall.sh --with-udev      # also the udev rule in /etc
sudo tools/uninstall.sh --with-package   # also a system-wide .deb install
```

`make uninstall` runs that script, and `make uninstall UNINSTALL_ARGS=--with-udev`
passes a flag through. The two root-only flags are checked before anything is
removed, so forgetting `sudo` fails with a message instead of leaving a
half-removed installation, and the script removes the *invoking* user's files
when run through `sudo` rather than looking in root's home directory.

If you installed an earlier version, its `~/.config/attackshark-x11/dpi.conf`
and `~/.local/state/attackshark-x11/stage` are not yours any more: nothing reads
them, and this app will not start from stale numbers. Deleting them is safe.

`doctor` is the quickest way to tell an installation problem from a mouse that is
simply asleep: it prints one line per check and exits non-zero only when
something actually stops the app from working. It recognises both installs, so
either `/usr/bin/attackshark-x11-tray` from the package or
`~/.local/bin/attackshark-x11-tray` from the source install counts as present.

### The udev rule, and why it looks the way it does

**The rule is still required, and nothing about the feature-report protocol
changes that.** `/dev/hidraw*` is `crw------- root root` by default, so without
it the app cannot even `open()` the receiver — every read, every write and every
announcement depends on the same access. Rule 1 is the load-bearing one; rule
2's symlink is only convenience, since the app also finds the node by scanning
sysfs for `1d57:fa60`.

Two things about `udev/99-attackshark-x11.rules` are deliberate:

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
| `-read-dpi` | read the per-stage DPI values, the selected stage and the polling rate from the mouse when it is opened (default on) |
| `-poll-timeout` | how long a single device read may block |
| `-retry-every` | delay before re-opening the receiver after it disappears |
| `-no-notify` | suppress desktop notifications |
| `-v` | log device activity to stderr |

The menu shows the live battery reading, the DPI stage with its value, the
connection state, a **DPI stages** submenu listing all six stages with the
current one ticked, a **DPI value** submenu that sets the current stage's value
in 2000 DPI steps, and a **Polling rate** submenu listing the four rates; clicking
one switches the mouse to it (see above). There is no config menu and no file to
edit: every number on screen came from the device. On quit, replacing the
receiver, or unplugging it, the icon turns grey and the app reopens the device
when it returns — no restart needed.

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

**The first reading after connecting is held back.** The firmware is known to
report `100 %` before its gauge has settled, so the first frame after each device
open is logged but not shown, and neither the tooltip nor the "Battery is full"
alert sees it. The next heartbeat, two seconds later, is what goes on screen. If
that frame never arrives the tray keeps saying the battery is waiting rather
than showing a number it cannot vouch for.

**A completed charge is now a reported state.** The charge byte is `0x02` when
charging finishes, and the tooltip and menu say `100 % (fully charged)` instead
of `(unknown)`; the notification fires from that frame rather than from the
"≥ 99 % while charging" guess, which is kept only as a fallback for when the
frame is not delivered.

**The stage is read, then followed.** The report read above also says which
stage the mouse has selected, so the menu names it from the first device open —
no press of the mouse's DPI button needed. After that the mouse announces a new
stage whenever it changes, and picking one from the menu confirms it, so the
label keeps tracking the device.

If that startup read does not happen — `-read-dpi=false`, older firmware, a
sleeping mouse — nothing is filled in from a previous run: the stage row reads
`DPI: unknown` until the mouse announces a stage change, because a stage
remembered from the last session is not evidence about this one. Nothing is
written to disk, so there is no state file to go stale.

### probe tool

```bash
attackshark-probe info       # discovery, interface numbers, descriptor size
attackshark-probe reportmap  # every input/output/feature report the descriptor declares
attackshark-probe battery    # wait for the next heartbeat, print it
attackshark-probe stage      # wait for a stage announcement (tap the DPI button)
attackshark-probe dpi        # read the stored per-stage DPI values from the device
attackshark-probe set-stage  # switch the selected DPI stage, waiting for the confirmation
attackshark-probe polling    # read the polling rate the device is set to
attackshark-probe set-polling # switch the polling rate, waiting for the confirmation
attackshark-probe set-dpi    # set the DPI value of a stage, waiting for the confirmation
attackshark-probe stream     # decode every status report as it arrives
attackshark-probe dump       # hexdump every frame, for reverse engineering
attackshark-probe doctor     # check the installation, not the device
```

Everything except `dpi`, `polling`, `set-stage`, `set-dpi` and `set-polling` is
read-only. `dpi` and `polling` send the `0xA0` permission request so the firmware
will answer a read, which changes no setting. The three `set-` commands are the
ones that do: `set-stage` and `set-dpi` read the configuration, change the
selected stage or that stage's DPI value, and write it back; `set-polling` writes
the nine-byte polling-rate report. All three print the device's confirmation and
read the setting back.
`-changes-only` on `stream` filters out repeat heartbeats. `stage` prints the
stage number, and points at `dpi` for the value behind it.

`doctor` is the one to reach for when the tray shows nothing, or shows less than
you expect. It checks the udev rule and its symlink, opens the receiver the way
the app does and waits for one report, looks for the installed tray binary,
reports whether the desktop has a pointer-speed setting the tray can use, and
asks the session bus whether notifications have a daemon to reach. Each line is `ok`, `warn` or `FAIL`: a
warning is something the app copes with (a missing symlink, a mouse that is
asleep), a failure is something that stops it, and only failures make the command
exit non-zero.

## The protocol

Reverse engineered on real hardware: Attack Shark X11, 2.4 GHz receiver,
**USB `1d57:fa60`** (the receiver identifies itself as `Xenta 2.4G Wireless
Device`). It is a four-interface composite HID device; only **USB interface #2**
carries the vendor channel described here.

The channel pushes **HID report ID `0x03`, five bytes**, with three subtypes:

```
03 55 40 01 4e     battery:  byte 3 = charge state, byte 4 = percent
                   (0x01 discharging, 0x03 charging, 0x02 fully charged,
                    0x4e = 78 %) pushed every ~2.00 s while the mouse is awake
                   the 0x40 may arrive as 0x41, documented upstream as the same
                   event and accepted here for that reason

03 55 10 03 00     DPI stage: byte 3 = current stage, 1..6
                   pushed only when the stage changes

03 55 50 00 04     feature-report reply: byte 3 = status, byte 4 = report id
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
values, which is why the live display needs no writes and no polling, and why
the app runs unprivileged once the udev rule is in place. Only the stored DPI
configuration has to be asked for, as described above.

`internal/mouse` is the whole decoder, and it is deliberately strict — it
requires report ID `0x03`, the `0x55` magic and an exact length, and passes
through an out-of-range stage instead of silently dropping it.

## Negative results: what does *not* work

Documented because each of these looks like it should work, and rediscovering
that it does not is expensive. The first one has since been resolved; it is kept
with the correction, because the wrong conclusion was believed for a while.

### Feature reads without the mailbox return unaddressable data

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

**Resolved.** The reads were being made without asking first. Report `0xA0` is a
read-permission mailbox: one `SET_REPORT` to it grants exactly one read, and
until it is granted the firmware returns residue. See
[DPI values](#dpi-values-what-the-device-holds-and-how-this-app-reads-it). What
remains true is that a read has to be both addressed and permitted; a bare
`HIDIOCGFEATURE` is still worthless on this receiver.

### The descriptor declares no output reports

Read from the hardware, the configuration interface's 230-byte report
descriptor declares input reports with IDs `0x01`, `0x02` and `0x03`, **no
output reports at all**, and 24 feature reports (`0x04`…`0x2E`, plus `0xA0`)
with lengths from 6 to 133 bytes. `attackshark-probe reportmap` prints exactly
that, item by item, straight off the device.

There is therefore no output-report channel on this device — but that is not the
same as no way to write. Feature reports are writable with `SET_REPORT`, and
that is the direction the vendor software uses; the lack of an output report
only rules out tools that insist on one. This app writes only the `0xA0`
permission request and, when asked to switch, the configuration report with a
changed stage byte, as explained above.

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
  starts. "Full" comes from the mouse's own `0x02` frame, with the ≥ 99 % while
  charging reading as a fallback for when that frame is missed. A post that
  fails is retried once after five minutes, and the notifier refuses to try again
  for that long in between: a desktop whose notification daemon is missing, or has
  just been restarted, loses one warning rather than every notification for the
  rest of the session.
- **Two writes, both feature reports.** `hidraw.SetFeature` sends the `0xA0`
  read-permission request and, when a stage is clicked, the 56-byte `0x04`
  configuration with only its stage byte changed. Both go through
  `internal/mouse`, both are refused when the configuration cannot be read, and
  no other package writes to the device. Keeping the write surface to one ioctl
  and two frames is what makes that checkable.
- **A wrong label is worse than no label.** That principle is what keeps the
  device read strictly validated (report ID, checksum, known codes, range) and
  why a failure falls back to the config file instead of guessing. It is also
  why the menu tick only moves on the device's confirmation. The tests assert
  all of it: a bad report is refused, an unconfigured mapping never produces a
  number in the tooltip, and a rejected or unconfirmed switch leaves the stage
  where it was.

## Credits

Three community projects made this much faster to write, and all are worth
looking at:

- [HarukaYamamoto0/attack-shark-x11-driver](https://github.com/HarukaYamamoto0/attack-shark-x11-driver) —
  the reverse engineering that made the DPI read possible: the `0xA0`
  permission mailbox, the `0x04` configuration report's layout and checksum,
  and the PAW3311 DPI code table used by `internal/mouse`. Its protocol notes
  are the reason the negative result above could be corrected.
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
attaches both to the GitHub release. The parsers are fuzzed as well as unit
tested, because a byte stream from the mouse is untrusted input:

```bash
go test ./internal/mouse -run='^$' -fuzz=FuzzParseBattery -fuzztime=30s
go test ./internal/mouse -run='^$' -fuzz=FuzzParseDPIConfig -fuzztime=30s
go test ./internal/mouse -run='^$' -fuzz=FuzzParseCommandConfirmation -fuzztime=30s
```

A failing input is written to `testdata/fuzz/<target>/` and is a permanent case
from then on; the seed corpus in each test holds frames captured from real
hardware.

The icon can be rendered without a running tray, which is how the image at the
top of this file is regenerated:

```bash
go run ./cmd/attackshark-icons -out docs/icon-states.png    # the strip above

go run ./cmd/attackshark-icons -size 22 -scale 8 -html /tmp/icons.html
                                                           # standalone page, both panels
```
