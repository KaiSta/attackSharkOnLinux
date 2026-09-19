package trayapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// lockFileName is the file the single-instance lock is taken on. It lives
// beside nothing else the app owns: it belongs to the running process, not to
// the installation, and the kernel drops the lock the moment that process ends.
const lockFileName = "attackshark-x11-tray.lock"

// lockDir is where the lock lives: the session's runtime directory when there
// is one, and a per-user state directory otherwise. A shared fallback such as
// /tmp is deliberately not used, because there two users on one machine would
// block each other's tray.
func lockDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".local", "state", "attackshark-x11")
}

// lockPath is the lock file acquireSingleInstanceLock uses. It is a variable so
// tests can take the lock in a temporary directory instead of the developer's
// own session, which may well be running the app.
var lockPath = func() string { return filepath.Join(lockDir(), lockFileName) }

// acquireSingleInstanceLock takes an exclusive lock, so that a second tray app
// exits instead of appearing beside the first. Two instances are not merely
// untidy: both open the receiver and compete for the same reports, which makes
// the reading on screen flicker between them.
//
// The returned function releases the lock. A crash cannot leave a stale lock
// behind, because the kernel releases it when the process dies - the lock is on
// the open descriptor, not on the file's contents.
func acquireSingleInstanceLock() (release func(), err error) {
	path := lockPath()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s for the single-instance lock: %w", dir, err)
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the single-instance lock %s: %w", path, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("attackshark-x11-tray is already running (lock %s)", path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	// The contents are diagnostics only - a PID to look up in ps - because the
	// lock itself, not the file, is what decides whether the app may run.
	_ = file.Truncate(0)
	_, _ = file.WriteString(strconv.Itoa(os.Getpid()) + "\n")

	return func() {
		// Unlocking explicitly keeps the release readable; closing the
		// descriptor alone would be enough.
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}
