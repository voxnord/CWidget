//go:build windows

// Package popup contains platform-specific window tweaks: hiding the window
// from the taskbar, locking its size, rounding corners, tray-side positioning
// and focus activation. All operations locate the window by its unique title.
package popup

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
    user32                   = windows.NewLazySystemDLL("user32.dll")
    gdi32                    = windows.NewLazySystemDLL("gdi32.dll")
    dwmapi                   = windows.NewLazySystemDLL("dwmapi.dll")
    procFindWindow           = user32.NewProc("FindWindowW")
    procGetWindowLongW       = user32.NewProc("GetWindowLongW")
    procSetWindowLongW       = user32.NewProc("SetWindowLongW")
    procGetWindowRect        = user32.NewProc("GetWindowRect")
    procSetWindowPos         = user32.NewProc("SetWindowPos")
    procSystemParametersInfo = user32.NewProc("SystemParametersInfoW")
    procShowWindow           = user32.NewProc("ShowWindow")
    procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
    procSetWindowRgn         = user32.NewProc("SetWindowRgn")
    procCreateRoundRectRgn   = gdi32.NewProc("CreateRoundRectRgn")
    procDwmSetWindowAttr     = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
    // GWL_EXSTYLE = -20, GWL_STYLE = -16 — in unsigned 32-bit form
    // (negative constants cannot be written into a uintptr).
    gwlExStyle     uintptr = 0xFFFFFFEC
    gwlStyle       uintptr = 0xFFFFFFF0
    wsExToolWindow         = 0x00000080 // removes the taskbar and Alt+Tab button
    wsExAppWindow          = 0x00040000 // forced taskbar button — we clear it
    wsThickFrame           = 0x00040000 // WS_THICKFRAME: invisible resize frame
    wsMaximizeBox          = 0x00010000 // WS_MAXIMIZEBOX: allows maximize (Win+Up)

    hwndTopmost   = ^uintptr(0) - 0 // HWND_TOPMOST  (-1)
    hwndNotopmost = ^uintptr(1) - 0 // HWND_NOTOPMOST (-2)

    swpNoSize       = 0x0001
    swpNoMove       = 0x0002
    swpNoZOrder     = 0x0004
    swpNoActivate   = 0x0010
    swpFrameChanged = 0x0020
    spiGetWorkArea  = 0x0030
    swRestore       = 9

    // Windows 11: rounding via DWM (anti-aliased).
    dwmwaCornerPref = 33
    dwmwcpRound     = 2

    trayMargin = 12 // margin from the work-area edges, px

    // The rounding radius scales with the window width (DPI is handled
    // automatically because we compute from the physical pixels of
    // GetWindowRect).
    //
    // The region is deliberately SMALLER than the CSS radius (13 vs 14 in
    // style.css), which makes it a strict superset of the painted rounded
    // area: the hard 1-bit GDI cut passes only through fully transparent
    // pixels and is never seen. The visible edge is the browser-antialiased
    // CSS arc on #app; this region is a pure safety net (e.g. against a
    // frame flash before the webview paints).
    cornerNum = 13.0
    cornerDen = 380.0
)

type rect struct {
    Left, Top, Right, Bottom int32
}

func utf16Ptr(s string) uintptr {
    p, err := windows.UTF16PtrFromString(s)
    if err != nil {
        return 0
    }
    return uintptr(unsafe.Pointer(p))
}

func findHwnd(title string) uintptr {
    hwnd, _, _ := procFindWindow.Call(0, utf16Ptr(title))
    return hwnd
}

func windowSize(hwnd uintptr) (int32, int32) {
    var wr rect
    procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
    return wr.Right - wr.Left, wr.Bottom - wr.Top
}

/* -------------------------------- taskbar -------------------------------- */

// RemoveFromTaskbar finds the Wails window by title and turns it into a
// tool-window. The window is created hidden (StartHidden: true), so the
// style is applied BEFORE the first show — a taskbar button never appears.
func RemoveFromTaskbar(title string) {
    go func() {
        for i := 0; i < 100; i++ {
            if hwnd := findHwnd(title); hwnd != 0 {
                st, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle)
                ns := (st | wsExToolWindow) &^ wsExAppWindow
                procSetWindowLongW.Call(hwnd, gwlExStyle, ns)
                return
            }
            time.Sleep(100 * time.Millisecond)
        }
    }()
}

/* ----------------------------- fixed size -------------------------------- */

// LockResize makes the window strictly fixed-size. WS_THICKFRAME provides an
// invisible resize frame: DefWindowProc returns hit-test zones near the edges
// even for frameless windows. WS_MAXIMIZEBOX is also cleared so the window
// cannot be maximized (Win+Up). The programmatic WindowSetSize (widget <->
// settings) goes through SetWindowPos and does not depend on these styles.
func LockResize(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    st, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
    ns := st &^ wsThickFrame &^ wsMaximizeBox
    if ns == st {
        return
    }
    procSetWindowLongW.Call(hwnd, gwlStyle, ns)
    procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
        swpFrameChanged|swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate)
}

/* ------------------------------- position -------------------------------- */

// MoveNearTray places the window in the bottom-right corner of the work area
// — the standard position for tray popups (like Windows notifications).
func MoveNearTray(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    w, h := windowSize(hwnd)
    if w <= 0 || h <= 0 {
        return
    }
    var wa rect
    procSystemParametersInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
    if wa.Right <= wa.Left || wa.Bottom <= wa.Top {
        return
    }
    x := wa.Right - w - trayMargin
    y := wa.Bottom - h - trayMargin
    procSetWindowPos.Call(hwnd,
        0, // HWND_NOTOPMOST
        uintptr(x), uintptr(y), 0, 0,
        swpNoSize|swpNoZOrder|swpNoActivate)
}

/* ------------------------------- rounding -------------------------------- */

// RoundCorners rounds the window corners.
// Windows 11 — native DWM rounding (smooth, survives resizes).
// Windows 10 — no system support (DWM rejects the attribute), so the window
// is clipped with a SetWindowRgn region; the edge has no anti-aliasing —
// a Win10 limitation — hence the slightly smaller radius (see cornerNum).
// The region is computed from the actual RECT: it is recomputed after a mode
// (window size) change; the call is idempotent.
func RoundCorners(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    if tryDWMCorners(hwnd) {
        return
    }
    applyRegion(hwnd)
    // WindowSetSize in Wails is asynchronous: the region may have been
    // computed from the old size. Recompute a couple more times from the
    // up-to-date RECT.
    go func() {
        for _, d := range []time.Duration{150 * time.Millisecond, 400 * time.Millisecond} {
            time.Sleep(d)
            applyRegion(hwnd)
        }
    }()
}

func tryDWMCorners(hwnd uintptr) bool {
    pref := uint32(dwmwcpRound)
    hr, _, _ := procDwmSetWindowAttr.Call(hwnd, dwmwaCornerPref, uintptr(unsafe.Pointer(&pref)), 4)
    return hr == 0 // S_OK only on Windows 11
}

func applyRegion(hwnd uintptr) {
    w, h := windowSize(hwnd)
    if w <= 0 || h <= 0 {
        return
    }
    r := int32(float64(w) * cornerNum / cornerDen)
    if r < 2 {
        r = 2
    }
    rgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), uintptr(r), uintptr(r))
    if rgn == 0 {
        return
    }
    // The system takes ownership of the region — do not delete it.
    procSetWindowRgn.Call(hwnd, rgn, 1)
}

/* -------------------------------- focus ---------------------------------- */

// FocusWindow activates the window (focus + foreground). Wails v2 has no
// equivalent (WindowFocus appeared only in v3), hence Win32. Best-effort:
// Windows restricts foreground transfers to "foreign" processes, but the
// process owning the tray icon the user just clicked is allowed to do this.
func FocusWindow(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    procShowWindow.Call(hwnd, swRestore) // restore if minimized
    procSetForegroundWindow.Call(hwnd)
}

/* ------------------------------- topmost --------------------------------- */

// SetTopmost toggles the WS_EX_TOPMOST style (always on top). A hidden
// window does not need the style: ShowMode re-applies it on every show.
// Best-effort like the rest of the package.
func SetTopmost(title string, enable bool) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    if enable {
        procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0,
            swpNoSize|swpNoMove|swpNoActivate)
        return
    }
    procSetWindowPos.Call(hwnd, hwndNotopmost, 0, 0, 0, 0,
        swpNoSize|swpNoMove|swpNoActivate)
}