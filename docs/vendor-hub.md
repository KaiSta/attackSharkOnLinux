# The vendor's browser hub, and why it cannot configure this mouse

Everything below was checked against the live vendor site and this machine's
hardware on 2026-09-18. The commands to reproduce each finding are at the end.

## Verdict

Attack Shark's browser configuration tool — **ATTACK SHARK HUB**, served at
<https://controlhub.top/AttackShark/> — **does not support the X11's 2.4 GHz
receiver**, and this is not a Linux problem. It would fail identically on
Windows, in the same Chromium, for two independent reasons:

1. The receiver's USB vendor ID is not in the hub's device database, so the
   hub never even offers the device for selection.
2. The hub can only bind to a HID device that declares an **output report with
   report ID 8**. This receiver declares **no output reports at all**.

There is therefore no route to the six per-stage DPI values through the hub.

## The hub itself

| | |
|---|---|
| URL | `https://controlhub.top/AttackShark/` |
| Page title | `Control HUB WEB` |
| Application name | `ATTACK SHARK MOUSE HUB`, version `1.2.0` |
| Bundle | a single Vite-built script, 834,303 bytes |
| Deployment | **one** — the site root is `Compx HUB WEB` |

There is no per-model deployment. `/X11/`, `/x11/`, `/AttackSharkX11/`,
`/X11Ultra/` and `/X11SE/` all return the same 608-byte single-page-app shell,
and only `/AttackShark/cfg.json` exists as a real file. Every path on that host
serves the same application.

## Why it cannot see this receiver

### 1. The hub's device database does not contain the X11

The hub fetches its device database from `cfg.json`, resolved relative to the
page URL. Its contents:

```
title   ATTACK SHARK MOUSE HUB   version 1.2.0
vid     ["0x3554"]                       <- one vendor ID, not 0x1d57
pid     mouse.wireless  0xFB43 0xFB44 0xF517 0xF50D 0xF510
        mouse.wired     0xF5F6 0xF515 0xF50E 0xF516
        keyboard.*      0xF809 0xF50A 0x024F
mouse   17 profiles: sensors 3950 (maxDpi 42000) and 3955 (maxDpi 52000-60000)
```

The X11's receiver is USB **`1d57:fa60`** — a different vendor ID entirely, from
a different chip vendor (`Xenta`). The hub builds its WebHID device filter from
those VID/PID pairs, so it never opens a device chooser containing our device.

It is worth being precise about one near-miss: the hub's separate sensor library
`sensor.json` **does** define a `3311` entry — the X11's PixArt PAW3311 — with
its DPI encoding ranges (50-10000 step 50, 10100-12000, 12100-20000,
20200-24000, each with its own `DPIex` multiplier). So the hub knows the X11's
sensor. It simply has no *device profile* that pairs that sensor with this
receiver's VID, and — see below — no profile could bind to it anyway.

### 2. The hub requires an output report that this device does not have

This is the part that makes the answer OS-independent. The hub binds to a
device like this:

```js
for (let o = 0; o < device.collections.length; o++)
  device.collections[o].inputReports.length === 1 &&
  device.collections[o].outputReports.length === 1 &&
  (Wt == device.collections[o].outputReports[0].reportId && (je = device, ...))
```

`Wt` is `8`. So a collection must expose exactly one input report and exactly
one output report, and that output report must have report ID 8. All of the
hub's device traffic then flows through `sendReport(8, ...)`, and replies are
read from the input report of the same collection.

This receiver's own report descriptor, read from the hardware
(`attackshark-probe reportmap`):

```
device          /dev/attackshark-x11
descriptor      230 bytes
report ids      explicit (declared with 0x85 items)
input reports
  id 0x01         8 bits    1 bytes
  id 0x02        16 bits    2 bytes
  id 0x03        32 bits    4 bytes
output reports   none
feature reports
  id 0x04       440 bits   55 bytes
  ...
  id 0x08       464 bits   58 bytes
  ...
  id 0xa0        56 bits    7 bytes
```

**`output reports: none`.** Not just "no report ID 8" — there is no output
report in the descriptor at all, on any interface. And report ID 8 does exist,
but as a **58-byte feature report** in the vendor block, which is a different
HID direction: `sendReport` cannot address it.

Chrome's WebHID enumeration of the live device says the same thing from the
other side, and is in `tools/webhid-probe`'s output: the four collections report
`in=0 out=0 feature=0`, `in=1 out=0`, `in=1 out=0`, `in=0 out=0 feature=24`.

**What a Linux-specific failure would have looked like.** If this had been a
platform problem, the device would have been selectable and the failure would
have appeared when sending or reading reports — permissions on `/dev/hidraw*`,
`EBUSY` from a kernel driver owning the interface, or a WebHID call rejected.
None of that is what happens. The hub's own database never lists the device, and
the binding predicate it applies to every device it is given is false for this
one. Both facts travel with the device and the application, not with the OS.

## What the vendor does ship for the X11

The X11 has its own download, and it is not the hub:

| | |
|---|---|
| Software | `ATTACKSHARKX11.zip`, 6,655,346 bytes, modified 2026-04-17 |
| Contents | one file: `ATTACK SHARK X11 SOFT.exe`, 7,618,008 bytes |
| Format | PE32 (i386) GUI executable, 10 sections, Delphi/Embarcadero VCL markers |
| Payload | ~6.4 MB appended overlay, high entropy, no plaintext VID/PID or DPI strings |
| Manual | `X11UserManual.pdf`, one 4057x821 pt page, Adobe Illustrator + iLovePDF, no text layer and no embedded raster images (outlined vector art) |

So the vendor's supported configuration tool for this model is a **Windows-only
native application**, not a WebHID page. It cannot run here, and its constants
are packed rather than plaintext, so it is not a mechanical source of a factory
table either. Even if it were, per-stage values are user-adjustable, so a
factory table would not describe any particular mouse after someone has changed
a stage — including yours.

## Consequence for the DPI values

`README.md` still stands on the part it can: the value behind each stage is not
published, and the mouse never *pushes* it on its status channel. It is read
from the device's feature reports after the `0xA0` permission handshake
(`attackshark-probe dpi`), which is a different channel from everything the hub
binds to. What this document adds is that the hub is not a way around that, and
neither is the manual.

The remaining untouched lead is the receiver's own vendor channel: report ID
`0x08` is declared as a 58-byte feature report, and the hub's read command
format is known (`[0x08, 0x00, address_hi, address_lo, length, ...]` with a
`0x55`-based checksum). A *query* would mean sending a feature report to the
device, and the response channel for this dongle is unproven. Nothing here does
it: the only frames this project writes are the `0xA0` read-permission request
and, when a setting is picked from the tray, the configuration report with its
stage byte or one stage's DPI codes changed, or the nine-byte polling-rate
report. The hub's query path is not implemented (see the "DPI values" section of
README.md).

## Reproducing these findings

```bash
# The hub, its database, and its sensor library
curl -sSL https://controlhub.top/AttackShark/ | grep -o 'assets/index[^"]*'
curl -sSL https://controlhub.top/AttackShark/cfg.json | python3 -m json.tool | head -20
curl -sSL https://controlhub.top/AttackShark/sensor.json | python3 -c 'import json,sys; print(list(json.load(sys.stdin)))'

# There is only one deployment: every model path is the same 608-byte shell
for p in X11 AttackSharkX11 X11Ultra; do curl -sSo /dev/null -w "$p %{size_download}\n" https://controlhub.top/$p/; done

# This receiver's own reports, from the device
attackshark-probe reportmap

# The X11's own (Windows-only) software and manual
curl -sSIL https://download.attackshark.pro/ATTACKSHARK/X11/ATTACKSHARKX11.zip | grep -i content-length
```
