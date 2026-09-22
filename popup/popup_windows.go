//go:build windows

// Package popup contains platform-specific window tweaks: hiding the window
// from the taskbar, locking its size, rounding corners, tray-side
// positioning (taskbar-edge aware), focus activation and position
// persistence helpers. All operations locate the window by its unique title.
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
    shell32                  = windows.NewLazySystemDLL("shell32.dll")
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
    procMonitorFromRect      = user32.NewProc("MonitorFromRect")
    procMonitorFromPoint     = user32.NewProc("MonitorFromPoint")
    procGetMonitorInfo       = user32.NewProc("GetMonitorInfoW")
    procIsWindowVisible      = user32.NewProc("IsWindowVisible")
    procGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
    procSHAppBarMessage      = shell32.NewProc("SHAppBarMessage")
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

    // Taskbar edges (Win32 ABE_* values) and SHAppBarMessage command.
    abeLeft   = 0
    abeTop    = 1
    abeRight  = 2
    abeBottom = 3
    abmGetTaskbarPos   = 5

    monitorDefaultToNull    = 0
    monitorDefaultToNearest = 2

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

type point struct {
    X, Y int32
}

// monitorInfo layout matches the Win32 MONITORINFO (4-byte alignment only).
type monitorInfo struct {
    CbSize    uint32
    RcMonitor rect
    RcWork    rect
    DwFlags   uint32
}

// appBarData layout matches the Win32 APPBARDATA on x64.
type appBarData struct {
    CbSize           uint32
    HWnd             uintptr
    UCallbackMessage uint32
    UEdge            uint32
    Rc               rect
    LParam           uintptr
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

// taskbarEdge detects which screen edge the (primary) taskbar is docked to.
// SPI_GETWORKAREA alone cannot answer this — it only gives the free
// rectangle, which is exactly why the widget used to spawn at the bottom
// even with a top-docked taskbar.
func taskbarEdge() int {
    sw, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
    sh, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN

    var ab appBarData
    ab.CbSize = uint32(unsafe.Sizeof(ab))
    if r, _, _ := procSHAppBarMessage.Call(abmGetTaskbarPos, uintptr(unsafe.Pointer(&ab))); r == 0 {
        return abeBottom // query failed — assume the classic bottom taskbar
    }

    tw := ab.Rc.Right - ab.Rc.Left
    th := ab.Rc.Bottom - ab.Rc.Top
    if tw >= th { // horizontal taskbar
        if int32(sh)/2 > ab.Rc.Top {
            return abeTop
        }
        return abeBottom
    }
    // vertical taskbar
    if int32(sw)/2 > ab.Rc.Left {
        return abeLeft
    }
    return abeRight
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

// CornerPosForSize computes the default popup position for a window of the
// given size: the work-area corner NEAREST TO THE TRAY. The notification
// area sits at the bottom of horizontal taskbars and at the bottom of
// left-docked ones, hence the mapping below. Works for any taskbar edge.
func CornerPosForSize(w, h int) (int, int, bool) {
    var wa rect
    procSystemParametersInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
    if wa.Right <= wa.Left || wa.Bottom <= wa.Top {
        return 0, 0, false
    }
    var x, y int32
    switch taskbarEdge() {
    case abeTop: // tray at the top -> top-right corner, below the taskbar
        x = wa.Right - int32(w) - trayMargin
        y = wa.Top + trayMargin
    case abeLeft: // tray at the bottom of the left dock -> bottom-left
        x = wa.Left + trayMargin
        y = wa.Bottom - int32(h) - trayMargin
    default: // abeBottom and abeRight -> bottom-right corner
        x = wa.Right - int32(w) - trayMargin
        y = wa.Bottom - int32(h) - trayMargin
    }
    return int(x), int(y), true
}

// MoveNearTray places the window at the tray-side corner for its current
// size (taskbar-edge aware).
func MoveNearTray(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    w, h := windowSize(hwnd)
    x, y, ok := CornerPosForSize(int(w), int(h))
    if !ok {
        return
    }
    procSetWindowPos.Call(hwnd, 0,
        uintptr(x), uintptr(y), 0, 0,
        swpNoSize|swpNoZOrder|swpNoActivate)
}

/* ------------------------- position persistence --------------------------- */

// GetWindowPos returns the current absolute window position
// (virtual-screen coordinates: negative X = a monitor to the left,
// X > primary width = a monitor to the right).
func GetWindowPos(title string) (int, int, bool) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return 0, 0, false
    }
    var wr rect
    procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
    return int(wr.Left), int(wr.Top), true
}

// SetWindowPosAbs restores an absolute position. Multi-monitor safe: the
// target rectangle is checked with MonitorFromRect(MONITOR_DEFAULTTONULL) —
// if it does not intersect ANY monitor (the monitor was disconnected since
// the save), the call reports false and the caller falls back to the
// default placement instead of losing the window off-screen.
func SetWindowPosAbs(title string, x, y int) bool {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return false
    }
    w, h := windowSize(hwnd)
    if w <= 0 || h <= 0 {
        return false
    }
    target := rect{Left: int32(x), Top: int32(y), Right: int32(x) + w, Bottom: int32(y) + h}
    mon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&target)), monitorDefaultToNull)
    if mon == 0 {
        return false
    }
    procSetWindowPos.Call(hwnd, 0,
        uintptr(x), uintptr(y), 0, 0,
        swpNoSize|swpNoZOrder|swpNoActivate)
    return true
}

// FitToWorkArea clamps a window of w×h anchored at x,y so it fully fits into
// the work area of the monitor that anchor belongs to. Used for the taller
// settings view: it must never dive under the taskbar (any taskbar edge) or
// off the screen. The monitor is resolved from an anchor POINT — unambiguous
// even when the taller frame straddles two monitors.
func FitToWorkArea(x, y, w, h int) (int, int, bool) {
    pt := point{X: int32(x + 20), Y: int32(y + 20)}
    // POINT is passed BY VALUE on x64: pack both int32 halves into one register.
    packed := uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32
    mon, _, _ := procMonitorFromPoint.Call(packed, monitorDefaultToNearest)
    if mon == 0 {
        return 0, 0, false
    }
    var mi monitorInfo
    mi.CbSize = uint32(unsafe.Sizeof(mi))
    if r, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
        return 0, 0, false
    }
    nx, ny := int32(x), int32(y)
    if maxX := mi.RcWork.Right - int32(w); nx > maxX {
        nx = maxX
    }
    if nx < mi.RcWork.Left {
        nx = mi.RcWork.Left
    }
    if maxY := mi.RcWork.Bottom - int32(h); ny > maxY {
        ny = maxY
    }
    if ny < mi.RcWork.Top {
        ny = mi.RcWork.Top
    }
    return int(nx), int(ny), true
}

// IsWindowVisible reports whether the window is currently shown. Used by the
// position watcher so that hidden windows (blur-hide, the close button)
// never trigger a save.
func IsWindowVisible(title string) bool {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return false
    }
    r, _, _ := procIsWindowVisible.Call(hwnd)
    return r != 0
}

/* ------------------------------- rounding -------------------------------- */

// RoundCorners rounds the window corners.
// Windows 11 — native DWM rounding (smooth, survives resizes).
// Windows 10 — no system support (DWM rejects the attribute), so the window
// is clipped with a SetWindowRgn region; the edge has no anti-aliasing —
// a Win10 limitation — hence the slightly smaller radius (see cornerNum).
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

// FocusWindow activates the window (focus + foreground). Best-effort:
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