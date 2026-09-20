//go:build !windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/voxnord/cwidget/config"
)

// acquireSingleInstance is the non-Windows guard: an exclusive flock on a
// lock file in the app data directory, held for the process lifetime.
func acquireSingleInstance() bool {
    dir, err := config.Dir()
    if err != nil {
        return true // fail open
    }
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return true // fail open (the config loader will surface the real error)
    }
    f, err := os.OpenFile(filepath.Join(dir, "cwidget.lock"),
        os.O_CREATE|os.O_RDWR, 0o600)
    if err != nil {
        return true // fail open
    }
    if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
        f.Close()
        log.Printf("[start] another instance is already running — exiting")
        return false
    }
    // Keep f open for the process lifetime; the lock dies with the process.
    return true
}