package trayapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Take the lock somewhere temporary: the real path belongs to whichever session
// is running the tests, which may be running the app itself. The directory is
// chosen once, because a test that takes the lock and then looks at the file
// would otherwise be looking in a new directory on every call.
func useTemporaryLock(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), lockFileName)
	original := lockPath
	lockPath = func() string { return path }
	t.Cleanup(func() { lockPath = original })
	return path
}

// The point of the lock: the second instance is refused while the first holds
// it, and allowed again once the first has released it. flock locks belong to the
// open descriptor, not to the process, so this holds for two descriptors in one
// process exactly as it does for two processes.
func TestOnlyOneInstanceAtATime(t *testing.T) {
	useTemporaryLock(t)

	release, err := acquireSingleInstanceLock()
	if err != nil {
		t.Fatalf("the first instance could not take the lock: %v", err)
	}

	second, err := acquireSingleInstanceLock()
	if err == nil {
		second()
		t.Fatal("a second instance took the lock while the first held it")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want it to say the app is already running", err)
	}

	release()

	second, err = acquireSingleInstanceLock()
	if err != nil {
		t.Fatalf("the lock outlived its release: %v", err)
	}
	second()

	if _, err := acquireSingleInstanceLock(); err != nil {
		t.Errorf("the lock outlived the second release: %v", err)
	}
}

// The lock has to be taken in a directory that may not exist yet: neither
// ~/.local/state nor a stale XDG_RUNTIME_DIR can be relied on.
func TestLockIsCreatedIncludingItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", lockFileName)
	original := lockPath
	lockPath = func() string { return path }
	t.Cleanup(func() { lockPath = original })

	release, err := acquireSingleInstanceLock()
	if err != nil {
		t.Fatalf("acquireSingleInstanceLock: %v", err)
	}
	defer release()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the lock file was not created at %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("lock file mode = %o, want no group or other access", mode)
	}
}

// XDG_RUNTIME_DIR is per-session and private, so it is the right place for the
// lock; a stale or unset value has to fall back to something equally private
// rather than to a world-writable directory.
func TestLockDirPrefersTheRuntimeDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if got := lockDir(); got != dir {
		t.Errorf("lockDir() = %q, want the runtime directory %q", got, dir)
	}

	missing := filepath.Join(dir, "gone")
	t.Setenv("XDG_RUNTIME_DIR", missing)
	if got := lockDir(); got == missing {
		t.Errorf("lockDir() = %q, want a fallback for a runtime directory that does not exist", got)
	}
}
