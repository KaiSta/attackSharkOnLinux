# bin/ — local scratch space

Everything in this directory is a working artefact rather than part of the
project: builds, captured device traffic, and downloads from the
reverse-engineering sessions described in the main README and in
[docs/vendor-hub.md](../docs/vendor-hub.md). Nothing here is installed, and
nothing here is needed to build or run the app.

Git ignores the directory apart from this file, so nothing below it is
committed. The files worth keeping are the ones the documentation's claims rest
on:

| file | what it is |
|---|---|
| `hub-bundle.js`, `hub-cfg.json`, `hub-sensor.json`, `hub-index.html` | the vendor's browser tool, *ATTACK SHARK HUB*, downloaded from `controlhub.top` and read to establish that it cannot drive this receiver: its device database lists a single vendor ID, and it binds only to collections with an output report. See `docs/vendor-hub.md`. |
| `webhid-links.log`, `webhid-probe.log`, `webhid-report.json`, `chromium-launch.log` | the WebHID probe's output and the Chromium run it was taken in |
| `driver-links.txt` | the vendor's driver, firmware and manual download listing |
| `x11-driver/` | what was downloaded from it: `ATTACK SHARK X11 SOFT.exe` (the Windows configuration tool that holds the per-stage DPI values), the manual PDF, and its image-only text |
| `battery-capture.txt`, `charge-test.txt`, `charge-watch.txt` | battery frames captured from the receiver, including the charge-state byte flipping within one heartbeat of the cable going in |
| `autostart-run.log`, `freshness-run.log`, `tray-run.log` | runs of the app itself |
| `reference-shark.jpg`, `reference-shark2.jpg` | fin silhouettes the icon shape was compared against |
| `icon-preview.html`, `icon-sheet.png`, `icon-states.png`, `reference-preview.html` | icon rendering iterations, as produced by `cmd/attackshark-icons` |
| `attackshark-*` | locally built binaries; regenerate with `go build -o bin/... ./cmd/...` |

Captured logs and JSON contain timestamps, device paths and USB identifiers from
the machine they were taken on. They are kept for provenance, not as test
fixtures — the tests carry their own captured frames inline, in
`internal/mouse/protocol_test.go`.
