//go:build linux

package popup

import (
	"os"
	"os/exec"
	"time"
)

// RemoveFromTaskbar sets the EWMH skip_taskbar state via wmctrl on X11
// (best-effort). There is no portable way on Wayland — the function simply
// does nothing there.
func RemoveFromTaskbar(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, err := exec.LookPath("wmctrl")
    if err != nil {
        return
    }
    go func() {
        for i := 0; i < 30; i++ {
            cmd := exec.Command(wmctrl, "-r", title, "-b", "add,skip_taskbar")
            if cmd.Run() == nil {
                return
            }
            time.Sleep(200 * time.Millisecond) // the window may not exist yet
        }
    }()
}

// MoveNearTray is a stub (tray-side positioning depends on the DE).
func MoveNearTray(title string) {}

// LockResize is a stub (fixed size depends on the WM; can be added via
// _MOTIF_WM_HINTS/xprop if needed).
func LockResize(title string) {}

// RoundCorners is a stub: rounding comes from the CSS border-radius, and its
// visibility depends on the compositor (window transparency).
func RoundCorners(title string) {}

// FocusWindow activates the window by title (X11, best-effort via wmctrl).
func FocusWindow(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, err := exec.LookPath("wmctrl")
    if err != nil {
        return
    }
    _ = exec.Command(wmctrl, "-a", title).Run()
}

// SetTopmost is a stub: always-on-top depends on the WM (can be added via
// wmctrl -b add,above on X11 if needed).
func SetTopmost(title string, enable bool) {}