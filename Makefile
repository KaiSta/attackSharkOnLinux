# Build, install and package the Attack Shark X11 tray monitor.
#
#   make build       the binaries into bin/, which .gitignore excludes
#   make check       gofmt, go vet and go test - the same three CI runs
#   make install     per-user install: ~/.local/bin, autostart entry, docs
#   make package     build dist/attackshark-x11_<version>_<arch>.deb
#   make uninstall   undo make install
#   make help        all of the above, plus the variables that change them
#
# Nothing here re-invents the per-user layout. `make install` and
# `make uninstall` call tools/install.sh and tools/uninstall.sh, which stay the
# single description of where a per-user install puts things. The .deb is the
# system-wide route instead, and it is staged from scratch below.

SHELL := /bin/bash
.SUFFIXES:

GO      ?= go
GOFLAGS ?=

# --- build --------------------------------------------------------------------

BIN_DIR  := bin
DIST_DIR := dist

TRAY  := $(BIN_DIR)/attackshark-x11-tray
PROBE := $(BIN_DIR)/attackshark-probe
ICONS := $(BIN_DIR)/attackshark-icons

# Deliberately no dependency tracking on the .go files: `go build` keeps its own
# cache, so a no-op rebuild costs a few hundred milliseconds, and it can never be
# wrong about whether something changed.
#
# -trimpath keeps the builder's home directory out of the binaries, so a package
# built here contains no trace of where it was built.
.PHONY: build
build:
	$(GO) build $(GOFLAGS) -trimpath -o $(TRAY) ./cmd/attackshark-x11-tray
	$(GO) build $(GOFLAGS) -trimpath -o $(PROBE) ./cmd/attackshark-probe
	$(GO) build $(GOFLAGS) -trimpath -o $(ICONS) ./cmd/attackshark-icons
	@echo "built $(TRAY)"
	@echo "built $(PROBE)"
	@echo "built $(ICONS)  (development tool: renders the icon strip in docs/)"

.PHONY: check
check:
	@unformatted="$$(gofmt -l .)"; \
	if [[ -n "$${unformatted}" ]]; then \
		echo "gofmt would reformat:"; \
		echo "$${unformatted}"; \
		exit 1; \
	fi
	@echo "gofmt    clean"
	$(GO) vet ./...
	$(GO) test ./...

.PHONY: fmt
fmt:
	gofmt -w .

# --- install ------------------------------------------------------------------

# Per-user and unprivileged: install.sh builds both binaries itself and writes
# the autostart entry, the documentation and a dpi.conf template. Device access
# is the one privileged step and is deliberately separate:
#
#     sudo tools/install-udev.sh
#
# The .deb below installs that rule for you, on machines that take one.
.PHONY: install
install:
	tools/install.sh

UNINSTALL_ARGS ?=

.PHONY: uninstall
uninstall:
	tools/uninstall.sh $(UNINSTALL_ARGS)

# --- package ------------------------------------------------------------------

# Binaries and udev rules go under /usr, which is where dpkg-deb expects them:
# /bin and /lib are symlinks into /usr on a merged-/usr system, and staging to
# the symlinked paths would produce a package that conflicts with itself.
PKG_NAME := attackshark-x11
PKG_DIR  := $(DIST_DIR)/$(PKG_NAME)
PKG_BIN  := $(PKG_DIR)/usr/bin
PKG_UDEV := $(PKG_DIR)/usr/lib/udev/rules.d
PKG_AUTO := $(PKG_DIR)/etc/xdg/autostart
PKG_DOC  := $(PKG_DIR)/usr/share/doc/$(PKG_NAME)

# The upstream version dpkg will record, in the order it is decided:
#
#   1. VERSION=... on the command line, for a one-off build.
#   2. the VERSION file at the top of the tree, which holds the version of the
#      next release and is what .github/workflows/release.yml requires the tag
#      to match.
#   3. `git describe --tags --always --dirty`, rewritten into something dpkg
#      accepts, when there is no VERSION file.
#
# A dpkg version must begin with a digit and cannot contain "-", because a
# hyphen separates the upstream version from the Debian revision - while git
# describe produces both that and a bare commit id. So "c79bdbc" becomes
# 0.0.0+gitc79bdbc and "v1.2.3-4-gabc" becomes 1.2.3+4+gabc. With no git and no
# VERSION file (a source tarball) it falls back to 0.1.0, rather than producing
# a version dpkg-deb would reject.
#
# No `case` here, and no ${v#v} either: an unbalanced ")" or a "#" inside
# $(shell ...) would end the function or start a comment, both of which fail
# silently and confusingly. sed does the same rewriting with neither.
VERSION ?= $(shell \
	v=""; if [ -r VERSION ]; then v="$$(tr -d '[:space:]' < VERSION)"; fi; \
	if [ -z "$$v" ]; then \
		v="$$(git describe --tags --always --dirty 2>/dev/null || true)"; \
		v="$$(printf '%s' "$$v" | sed -e 's/^v//' -e 's/^[^0-9]/0.0.0+git&/')"; \
	fi; \
	if [ -z "$$v" ]; then v=0.1.0; fi; \
	printf '%s' "$$v" | tr - '+' )

# dpkg names architectures differently from both Go and the kernel, and only
# for the architectures this could plausibly be built for. Both spellings are
# listed so that either an explicit GOARCH= from a cross-build or the machine's
# own `uname -m` resolves.
#
# uname rather than `go env GOARCH` on purpose: reading this file should not
# need the Go toolchain, and any `go` command writes its telemetry counters into
# $HOME, which `make help` has no business doing. A cross-build still works,
# because GOARCH is in the environment when it is set that way.
deb_amd64   := amd64
deb_x86_64  := amd64
deb_386     := i386
deb_i686    := i386
deb_arm     := armhf
deb_armv7l  := armhf
deb_arm64   := arm64
deb_aarch64 := arm64
deb_ppc64le := ppc64el
deb_riscv64 := riscv64
deb_s390x   := s390x

HOST_ARCH ?= $(shell uname -m)
ARCH      ?= $(or $(deb_$(GOARCH)),$(deb_$(HOST_ARCH)),$(HOST_ARCH))

# dpkg-deb refuses to build without a Maintainer, so take the one git already
# knows rather than inventing an address.
GIT_NAME  := $(shell git config user.name 2>/dev/null)
GIT_EMAIL := $(shell git config user.email 2>/dev/null)
MAINTAINER ?= $(if $(GIT_NAME),$(GIT_NAME) <$(GIT_EMAIL)>,Attack Shark X11 packagers <noreply@localhost>)

DEB := $(DIST_DIR)/$(PKG_NAME)_$(VERSION)_$(ARCH).deb

.PHONY: package
package: build
	@command -v dpkg-deb >/dev/null 2>&1 || { \
		echo "error: dpkg-deb is not installed, and this target builds a Debian package" >&2; \
		exit 1; \
	}
	rm -rf '$(PKG_DIR)'
	install -d '$(PKG_DIR)/DEBIAN' $(PKG_BIN) $(PKG_UDEV) $(PKG_AUTO) $(PKG_DOC)

	install -m 0755 $(TRAY) '$(PKG_BIN)/attackshark-x11-tray'
	install -m 0755 $(PROBE) '$(PKG_BIN)/attackshark-probe'
	install -m 0644 udev/99-attackshark-x11.rules '$(PKG_UDEV)/99-attackshark-x11.rules'

	install -m 0644 README.md '$(PKG_DOC)/README.md'
	install -m 0644 docs/vendor-hub.md '$(PKG_DOC)/vendor-hub.md'
	install -m 0644 examples/dpi.conf '$(PKG_DOC)/dpi.conf.example'

	# Written out here rather than copied from tools/install.sh, because the
	# packaged binary lives in /usr/bin instead of ~/.local/bin. Every other key
	# is identical to the per-user entry, on purpose.
	#
	# umask first: a redirection creates the file 0666 & ~umask, and a umask of
	# 002 would put a group-writable file into /etc.
	umask 022; printf '%s\n' \
		'[Desktop Entry]' \
		'Type=Application' \
		'Name=Attack Shark X11 tray' \
		'Comment=Battery level and DPI stage for the Attack Shark X11 mouse' \
		'Exec=/usr/bin/attackshark-x11-tray' \
		'Terminal=false' \
		'NoDisplay=false' \
		'X-GNOME-Autostart-enabled=true' \
		>'$(PKG_AUTO)/attackshark-x11-tray.desktop'

	install -m 0755 packaging/postinst '$(PKG_DIR)/DEBIAN/postinst'
	install -m 0755 packaging/postrm '$(PKG_DIR)/DEBIAN/postrm'
	install -m 0644 packaging/conffiles '$(PKG_DIR)/DEBIAN/conffiles'

	# One command writes the whole control file, so its quoting survives
	# `make -n` and a dry run shows exactly what would be written.
	@{ \
		printf 'Package: %s\n' '$(PKG_NAME)'; \
		printf 'Version: %s\n' '$(VERSION)'; \
		printf 'Architecture: %s\n' '$(ARCH)'; \
		printf 'Maintainer: %s\n' '$(MAINTAINER)'; \
		printf 'Installed-Size: %s\n' "$$(du -sk --exclude=DEBIAN '$(PKG_DIR)' | cut -f1)"; \
		printf 'Section: utils\n'; \
		printf 'Priority: optional\n'; \
		printf 'Depends: libc6, udev\n'; \
		printf 'Recommends: xdg-utils\n'; \
		printf 'Description: %s\n' 'battery level and DPI stage of an Attack Shark X11 in the tray'; \
		printf '%s\n' \
			' A tray icon showing the battery percentage and charge state of an Attack' \
			' Shark X11 mouse, and which of its six DPI stages is selected. The 2.4 GHz' \
			' receiver is read directly over hidraw, so nothing is polled and nothing is' \
			' requested from the device.' \
			' .' \
			' It only ever reads. The DPI button on the mouse stays the supported way to' \
			' change stages, and the per-stage DPI values come from' \
			' ~/.config/attackshark-x11/dpi.conf, for which the app writes a template and' \
			' never invents a number.' \
			' .' \
			' attackshark-probe is included for diagnosis: "attackshark-probe doctor"' \
			' checks device access, the udev rule and the session, and' \
			' "attackshark-probe reportmap" prints the report descriptor the receiver' \
			' declares.'; \
	} >'$(PKG_DIR)/DEBIAN/control'

	# --root-owner-group (dpkg-deb 1.19 and later) means no fakeroot is needed:
	# every staged file belongs to the building user, and dpkg-deb writes the
	# archive as root:root all the same.
	dpkg-deb --root-owner-group --build '$(PKG_DIR)' '$(DEB)'

	@echo "built $(DEB)"
	@echo "      install with:  sudo apt install ./$(DEB)"
	@echo "      inspect with:  dpkg-deb -c $(DEB)"

.PHONY: clean
clean:
	rm -rf '$(DIST_DIR)'
	rm -f $(TRAY) $(PROBE) $(ICONS)
	@echo "removed $(DIST_DIR)/ and the three binaries from 'make build'"
	@echo "left alone: everything else in bin/, which is also this project's"
	@echo "scratch space - captures, reference images, vendor bundles"

.PHONY: help
help:
	@printf '%s\n' \
		'usage: make [target]' \
		'' \
		'  build            build all three commands into bin/ (tray, probe, icons)' \
		'  check            gofmt -l ., go vet and go test - what CI runs' \
		'  fmt              gofmt -w the tree' \
		'  install          per-user install via tools/install.sh: no root needed' \
		'  package          build a Debian package in dist/, needs dpkg-deb' \
		'  uninstall        undo the per-user install via tools/uninstall.sh' \
		'  clean            remove dist/ and the binaries from make build' \
		'  help             this text' \
		'' \
		'  all is the default target, and it means build.' \
		'' \
		'variables:' \
		'' \
		'  VERSION=1.2.3            version recorded in the .deb (default: VERSION file)' \
		'  ARCH=arm64               architecture recorded in the .deb (default: uname -m)' \
		'  GOARCH=arm64             same, in Go spelling, as a cross-build sets it' \
		'  GO=/path/to/go           toolchain to build with' \
		'  GOFLAGS=-race            passed through to every go build, vet and test' \
		'  UNINSTALL_ARGS=--purge   passed on to tools/uninstall.sh' \
		'' \
		'installing:' \
		'' \
		'  make install     per-user, no root, into ~/.local/bin, with autostart entry' \
		'  make package     system-wide, one .deb, for every user on the machine' \
		'' \
		'  The two are alternatives rather than layers. Install both and ~/.local/bin' \
		'  shadows /usr/bin on most PATHs, while the single-instance lock the app takes' \
		'  means the loser exits quietly instead of drawing a second tray icon.' \
		'' \
		'  Device access is one privileged step either way. The .deb installs the udev' \
		'  rule itself; a source install needs it once, before the tray will see the' \
		'  mouse:' \
		'' \
		'      sudo tools/install-udev.sh' \
		'' \
		'README.md has the full story, including why the rule looks the way it does.'
