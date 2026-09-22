//go:build linux

package popup

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// trayMargin mirrors the Windows value: the gap between the window and the
// work-area edges when placed at the tray-side corner.
const trayMargin = 12

func haveWmctrl() (string, bool) {
    p, err := exec.LookPath("wmctrl")
    return p, err == nil
}

// RemoveFromTaskbar sets the EWMH skip_taskbar state via wmctrl on X11
// (best-effort). There is no portable way on Wayland.
func RemoveFromTaskbar(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, ok := haveWmctrl()
    if !ok {
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

// windowGeom parses `wmctrl -lG`: "<winid> <desktop> <x> <y> <w> <h> <host>
// <title>".
func windowGeom(title string) (x, y, w, h int, ok bool) {
    wmctrl, have := haveWmctrl()
    if !have {
        return 0, 0, 0, 0, false
    }
    out, err := exec.Command(wmctrl, "-lG").Output()
    if err != nil {
        return 0, 0, 0, 0, false
    }
    for _, line := range strings.Split(string(out), "\n") {
        if !strings.HasSuffix(strings.TrimSpace(line), title) {
            continue
        }
        f := strings.Fields(line)
        if len(f) < 6 {
            continue
        }
        xs, err1 := strconv.Atoi(f[2])
        ys, err2 := strconv.Atoi(f[3])
        ws, err3 := strconv.Atoi(f[4])
        hs, err4 := strconv.Atoi(f[5])
        if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
            continue
        }
        return xs, ys, ws, hs, true
    }
    return 0, 0, 0, 0, false
}

// workArea parses the desktop work area from `wmctrl -d` (the "WA:" field),
// preferring the active desktop line. This at least respects the taskbar /
// panel SIZE; the panel EDGE is not portably detectable on X11, so the
// tray-side corner logic stays Windows-only (honest limitation).
func workArea() (x, y, w, h int, ok bool) {
    wmctrl, have := haveWmctrl()
    if !have {
        return 0, 0, 0, 0, false
    }
    out, err := exec.Command(wmctrl, "-d").Output()
    if err != nil {
        return 0, 0, 0, 0, false
    }
    parse := func(line string) (int, int, int, int, bool) {
        f := strings.Fields(line)
        if len(f) < 9 || f[6] != "WA:" {
            return 0, 0, 0, 0, false
        }
        xy := strings.SplitN(f[7], ",", 2)
        wh := strings.SplitN(f[8], "x", 2)
        if len(xy) != 2 || len(wh) != 2 {
            return 0, 0, 0, 0, false
        }
        xs, e1 := strconv.Atoi(xy[0])
        ys, e2 := strconv.Atoi(xy[1])
        ws, e3 := strconv.Atoi(wh[0])
        hs, e4 := strconv.Atoi(wh[1])
        if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
            return 0, 0, 0, 0, false
        }
        return xs, ys, ws, hs, true
    }
    firstOK := false
    for _, line := range strings.Split(string(out), "\n") {
        x0, y0, w0, h0, ok0 := parse(line)
        if !ok0 {
            continue
        }
        if !firstOK {
            x, y, w, h, ok = x0, y0, w0, h0, true
            firstOK = true
        }
        if strings.Fields(line)[1] == "*" { // active desktop wins
            return x0, y0, w0, h0, true
        }
    }
    return x, y, w, h, ok
}

// CornerPosForSize returns the bottom-right corner of the desktop work area
// for a window of the given size. Limitation: on X11 the panel edge is not
// portably detectable, so this always uses the bottom-right corner of the
// work area (which is correct for bottom panels and still respects top
// panels' height).
func CornerPosForSize(w, h int) (int, int, bool) {
    ax, ay, aw, ah, ok := workArea()
    if !ok || aw <= 0 || ah <= 0 {
        return 0, 0, false
    }
    return ax + aw - w - trayMargin, ay + ah - h - trayMargin, true
}

// MoveNearTray places the window at the tray-side corner for its current
// size (best-effort, X11 only).
func MoveNearTray(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, have := haveWmctrl()
    if !have {
        return
    }
    _, _, w, h, ok := windowGeom(title)
    if !ok {
        return
    }
    cx, cy, ok2 := CornerPosForSize(w, h)
    if !ok2 {
        return
    }
    _ = exec.Command(wmctrl, "-r", title, "-e", fmt.Sprintf("0,%d,%d,-1,-1", cx, cy)).Run()
}

// LockResize is a stub (fixed size depends on the WM).
func LockResize(title string) {}

// RoundCorners is a stub: rounding comes from the CSS border-radius, and its
// visibility depends on the compositor (window transparency).
func RoundCorners(title string) {}

// FocusWindow activates the window by title (X11, best-effort via wmctrl).
func FocusWindow(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, ok := haveWmctrl()
    if !ok {
        return
    }
    _ = exec.Command(wmctrl, "-a", title).Run()
}

/* ------------------------- position persistence ---------------------------
 * Best-effort via wmctrl. The per-monitor check of the Windows
 * implementation is approximated by the desktop work area (wmctrl reports
 * one work area per desktop). */

// GetWindowPos returns the current absolute window position.
func GetWindowPos(title string) (int, int, bool) {
    x, y, _, _, ok := windowGeom(title)
    return x, y, ok
}

// SetWindowPosAbs moves the window to absolute coordinates (wmctrl -e; -1
// keeps the current size). Returns false when wmctrl is unavailable or on
// Wayland.
func SetWindowPosAbs(title string, x, y int) bool {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return false
    }
    wmctrl, ok := haveWmctrl()
    if !ok {
        return false
    }
    geom := "0," + strconv.Itoa(x) + "," + strconv.Itoa(y) + ",-1,-1"
    return exec.Command(wmctrl, "-r", title, "-e", geom).Run() == nil
}

// FitToWorkArea clamps the anchor so a w×h window fully fits into the
// desktop work area (wmctrl -d). ok=false when the work area is unknown.
func FitToWorkArea(x, y, w, h int) (int, int, bool) {
    ax, ay, aw, ah, ok := workArea()
    if !ok || aw <= 0 || ah <= 0 {
        return 0, 0, false
    }
    nx, ny := x, y
    if maxX := ax + aw - w; nx > maxX {
        nx = maxX
    }
    if nx < ax {
        nx = ax
    }
    if maxY := ay + ah - h; ny > maxY {
        ny = maxY
    }
    if ny < ay {
        ny = ay
    }
    return nx, ny, true
}

// IsWindowVisible is a stub: hidden windows do not change their position,
// so the watcher's baseline logic makes a real visibility check unnecessary
// on X11.
func IsWindowVisible(title string) bool { return true }