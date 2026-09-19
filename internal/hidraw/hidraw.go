// Package hidraw provides device discovery and raw HID access for the
// Attack Shark X11 receiver. It is read-only: nothing here writes to the
// device.
//
// The receiver presents four HID interfaces. Only USB interface #2 carries the
// vendor channel that pushes battery and DPI stage reports, and it is the only
// one this package ever opens.
//
// Access needs an ACL on /dev/hidraw*: by default those nodes are
// crw------- root root. Install the shipped udev rule
// (udev/99-attackshark-x11.rules) once, after which the desktop user can open
// the device with no privileges at all.
package hidraw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	// VendorID and ProductID identify the 2.4 GHz receiver.
	VendorID  = 0x1d57
	ProductID = 0xfa60

	// ConfigInterface is the USB interface number carrying the vendor
	// configuration channel and the battery/DPI status reports.
	ConfigInterface = 2

	// SymlinkPath is created by the shipped udev rule and is stable across
	// reconnects. hidraw node numbers are not: the same receiver has been
	// observed as hidraw2 and as hidraw3 on different machines.
	SymlinkPath = "/dev/attackshark-x11"

	// ReadBufferSize is comfortably larger than any report this device emits.
	ReadBufferSize = 64

	sysHidraw = "/sys/class/hidraw"
)

// Candidate is a hidraw node belonging to the receiver.
type Candidate struct {
	Path string
	Phys string
	// Interface is the USB interface number parsed from HID_PHYS, or -1 if it
	// could not be determined.
	Interface int
}

// ioctl request encoding, from <asm-generic/ioctl.h>.
const (
	iocNRBits    = 8
	iocTypeBits  = 8
	iocSizeBits  = 14
	iocDirBits   = 2
	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits
	iocSizeShift = iocTypeShift + iocTypeBits
	iocDirShift  = iocSizeShift + iocSizeBits

	iocNone  = 0
	iocWrite = 1
	iocRead  = 2
)

func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<iocDirShift | typ<<iocTypeShift | nr<<iocNRShift | size<<iocSizeShift
}

// hidType is the ioctl type byte for hidraw, 'H'.
const hidType = 'H'

// HID ioctl request numbers. size is the buffer length in bytes, including the
// report ID byte for numbered reports.
var (
	// HIDIOCGFEATURE reads a feature report. size must match the report length
	// declared in the device's descriptor, plus one for the ID byte.
	HIDIOCGFEATURE = func(size int) uintptr { return ioc(iocRead|iocWrite, hidType, 0x07, uintptr(size)) }
	// HIDIOCGINPUT reads an input report.
	HIDIOCGINPUT = func(size int) uintptr { return ioc(iocRead|iocWrite, hidType, 0x0A, uintptr(size)) }
	// HIDIOCGRDESCSIZE reads the report descriptor length.
	HIDIOCGRDESCSIZE = ioc(iocRead, hidType, 0x01, 4)
	// hidIOCGRDESC reads the raw report descriptor. Its argument is a fixed
	// struct hidraw_report_descriptor - a u32 length followed by a
	// HID_MAX_DESCRIPTOR_SIZE byte buffer - so the request number encodes the
	// size of that struct, never the length of the descriptor itself.
	hidIOCGRDESC = ioc(iocRead, hidType, 0x02, 4+hidMaxDescriptorSize)
)

// hidMaxDescriptorSize is HID_MAX_DESCRIPTOR_SIZE from the kernel's hid.h.
const hidMaxDescriptorSize = 4096

// ErrNotFound means no receiver matching VendorID/ProductID was found.
var ErrNotFound = errors.New("attack shark x11 receiver not found")

// Devices returns every hidraw node belonging to the receiver, with the
// configuration interface (USB interface #2) first. Reading sysfs needs no
// privileges, so discovery works before the udev rule is installed.
func Devices() ([]Candidate, error) {
	matches, err := filepath.Glob(sysHidraw + "/hidraw*")
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		return nodeNumber(matches[i]) < nodeNumber(matches[j])
	})

	var candidates []Candidate
	for _, node := range matches {
		props, err := readUevent(filepath.Join(node, "device", "uevent"))
		if err != nil {
			continue
		}
		if !matchHIDID(props["HID_ID"]) {
			continue
		}
		iface := -1
		if n, ok := interfaceFromPhys(props["HID_PHYS"]); ok {
			iface = n
		}
		candidates = append(candidates, Candidate{
			Path:      "/dev/" + filepath.Base(node),
			Phys:      props["HID_PHYS"],
			Interface: iface,
		})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w (no hidraw node reports HID_ID matching %04x:%04x)", ErrNotFound, VendorID, ProductID)
	}

	// Config interface first; otherwise keep numeric order.
	configRank := func(c Candidate) int {
		if c.Interface == ConfigInterface {
			return 0
		}
		return 1
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return configRank(candidates[i]) < configRank(candidates[j])
	})
	return candidates, nil
}

// Discover returns the path to prefer when opening the device: the udev
// symlink when it exists, otherwise the configuration interface discovered by
// scanning sysfs.
func Discover() (string, error) {
	if _, err := os.Stat(SymlinkPath); err == nil {
		return SymlinkPath, nil
	}
	candidates, err := Devices()
	if err != nil {
		return "", err
	}
	return candidates[0].Path, nil
}

// openOrder returns the paths to try when opening, most likely first.
func openOrder() ([]string, error) {
	var paths []string
	seen := map[string]bool{}
	if _, err := os.Stat(SymlinkPath); err == nil {
		paths = append(paths, SymlinkPath)
		seen[SymlinkPath] = true
	}
	candidates, err := Devices()
	if err != nil {
		return paths, err
	}
	for _, c := range candidates {
		if !seen[c.Path] {
			paths = append(paths, c.Path)
			seen[c.Path] = true
		}
	}
	return paths, nil
}

// Device is an open hidraw connection to the receiver's configuration
// interface.
type Device struct {
	path string
	f    *os.File
}

// Open opens the receiver's configuration interface, preferring the stable
// udev symlink and falling back to a sysfs scan.
func Open() (*Device, error) {
	paths, err := openOrder()
	if err != nil && len(paths) == 0 {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, ErrNotFound
	}

	var failures []string
	for _, p := range paths {
		dev, err := OpenPath(p)
		if err == nil {
			return dev, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", p, err))
	}
	return nil, fmt.Errorf("could not open any receiver hidraw node: %s", strings.Join(failures, "; "))
}

// OpenPath opens one specific hidraw node.
func OpenPath(path string) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("%w (install udev/99-attackshark-x11.rules - see README)", err)
		}
		return nil, err
	}
	return &Device{path: path, f: f}, nil
}

// Path returns the node this device was opened from.
func (d *Device) Path() string { return d.path }

// Close releases the device.
func (d *Device) Close() error {
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	return err
}

// ReadReport reads a single HID report, waiting at most timeout. It returns
// os.ErrDeadlineExceeded when nothing arrived in time, which callers use as a
// tick to check for shutdown or a vanished device.
func (d *Device) ReadReport(buf []byte, timeout time.Duration) (int, error) {
	if err := d.f.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	return d.f.Read(buf)
}

// WriteReport writes an output report. hidraw routes this to the device's
// output report mechanism, matching what hidapi's write() does on Linux.
func (d *Device) WriteReport(buf []byte) error {
	_, err := d.f.Write(buf)
	return err
}

// control runs fn with the raw file descriptor without disturbing the runtime
// poller, so read deadlines keep working.
func (d *Device) control(fn func(fd uintptr) error) error {
	raw, err := d.f.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := raw.Control(func(fd uintptr) { inner = fn(fd) }); err != nil {
		return err
	}
	return inner
}

func (d *Device) ioctlBuf(req uintptr, buf []byte) error {
	if len(buf) == 0 {
		return errors.New("hidraw: empty ioctl buffer")
	}
	return d.control(func(fd uintptr) error {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(&buf[0])))
		runtime.KeepAlive(buf)
		if errno != 0 {
			return errno
		}
		return nil
	})
}

// GetFeature reads a feature report. size is the total report length declared
// by the descriptor including the report ID byte, and buf[0] is the report ID.
//
// Note: this is here to document a negative result, not to be used. Reading any
// feature report on the X11 receiver returns unaddressable data, because the
// dongle ignores the requested report ID - see the negative results section of
// README.md before building anything on top of it.
func (d *Device) GetFeature(reportID byte, size int) ([]byte, error) {
	buf := make([]byte, size)
	buf[0] = reportID
	if err := d.ioctlBuf(HIDIOCGFEATURE(size), buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// GetInput reads an input report.
func (d *Device) GetInput(reportID byte, size int) ([]byte, error) {
	buf := make([]byte, size)
	buf[0] = reportID
	if err := d.ioctlBuf(HIDIOCGINPUT(size), buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// DescriptorSize returns the length of the device's HID report descriptor.
func (d *Device) DescriptorSize() (int, error) {
	var buf [4]byte
	if err := d.ioctlBuf(HIDIOCGRDESCSIZE, buf[:]); err != nil {
		return 0, err
	}
	return int(binary.NativeEndian.Uint32(buf[:])), nil
}

// Descriptor returns the raw HID report descriptor.
func (d *Device) Descriptor() ([]byte, error) {
	size, err := d.DescriptorSize()
	if err != nil {
		return nil, err
	}
	if size <= 0 || size > hidMaxDescriptorSize {
		return nil, fmt.Errorf("hidraw: implausible report descriptor size %d (the kernel reports at most %d)", size, hidMaxDescriptorSize)
	}

	// The kernel expects the descriptor length in the struct's leading u32, and
	// always copies out the whole struct, so the buffer is a fixed 4100 bytes.
	buf := make([]byte, 4+hidMaxDescriptorSize)
	binary.NativeEndian.PutUint32(buf[:4], uint32(size))
	if err := d.ioctlBuf(hidIOCGRDESC, buf); err != nil {
		return nil, err
	}
	if got := int(binary.NativeEndian.Uint32(buf[:4])); got > 0 && got <= hidMaxDescriptorSize {
		size = got
	}
	return buf[4 : 4+size], nil
}

func nodeNumber(path string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "hidraw"))
	if err != nil {
		return -1
	}
	return n
}

func readUevent(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	props := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		props[key] = value
	}
	return props, nil
}

// matchHIDID checks a uevent HID_ID value such as "0003:00001D57:0000FA60"
// against the receiver's vendor and product IDs.
func matchHIDID(id string) bool {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(id)), ":")
	if len(parts) != 3 {
		return false
	}
	vid, errV := strconv.ParseUint(parts[1], 16, 32)
	pid, errP := strconv.ParseUint(parts[2], 16, 32)
	if errV != nil || errP != nil {
		return false
	}
	return vid == VendorID && pid == ProductID
}

// interfaceFromPhys extracts the interface number from a HID_PHYS value such
// as "usb-0000:07:00.0-2/input2".
func interfaceFromPhys(phys string) (int, bool) {
	base := filepath.Base(strings.TrimSpace(phys))
	if !strings.HasPrefix(base, "input") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(base, "input"))
	if err != nil {
		return 0, false
	}
	return n, true
}
