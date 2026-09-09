//go:build windows

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
    // GWL_EXSTYLE = -20, GWL_STYLE = -16 — в беззнаковом 32-битном виде
    // (отрицательные константы в uintptr писать нельзя).
    gwlExStyle     uintptr = 0xFFFFFFEC
    gwlStyle       uintptr = 0xFFFFFFF0
    wsExToolWindow         = 0x00000080 // убирает кнопку из панели задач и Alt+Tab
    wsExAppWindow          = 0x00040000 // принудительная кнопка — снимаем её
    wsThickFrame           = 0x00040000 // WS_THICKFRAME: невидимая рамка ресайза
    wsMaximizeBox          = 0x00010000 // WS_MAXIMIZEBOX: разрешает развернуть (Win+Up)

    swpNoSize       = 0x0001
    swpNoMove       = 0x0002
    swpNoZOrder     = 0x0004
    swpNoActivate   = 0x0010
    swpFrameChanged = 0x0020
    spiGetWorkArea  = 0x0030
    swRestore       = 9

    // Windows 11: скругление средствами DWM (с антиалиасингом).
    dwmwaCornerPref = 33
    dwmwcpRound     = 2

    trayMargin = 12 // отступ от краёв рабочей области, px

    // Радиус скругления пропорционален ширине окна (пересчёт DPI автоматический,
    // т.к. считаем от физических пикселей GetWindowRect).
    //
    // ВАЖНО (антиступенька на Win10): регион чуть МЕНЬШЕ CSS-радиуса (13 против
    // 14 в style.css). Жёсткий край разреза — а GDI-регион всегда 1-битный,
    // без сглаживания — проходит ровно по тёмной hairline-линии, нарисованной
    // в CSS (body box-shadow inset). Ступенчатые пиксели попадают на тёмную
    // линию и становятся практически незаметны. Хотите вернуть прежний вид —
    // поставьте 14.0 и уберите box-shadow у body.
    cornerNum = 15.0
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

/* ------------------------------ таскбар --------------------------------- */

// RemoveFromTaskbar находит окно Wails по заголовку и превращает его в tool-window.
// Окно создаётся скрытым (StartHidden: true), поэтому стиль применяется
// ДО первого показа — кнопка в панели задач никогда не появится.
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

/* --------------------------- фиксированный размер ------------------------ */

// LockResize делает окно строго фиксированного размера.
// WS_THICKFRAME даёт невидимую рамку ресайза: DefWindowProc возвращает зоны
// захвата у краёв даже frameless-окна. Заодно снимаем WS_MAXIMIZEBOX,
// чтобы окно нельзя было развернуть (Win+Up). Программный WindowSetSize
// (виджет ↔ настройки) идёт через SetWindowPos и от этих стилей не зависит.
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

/* ------------------------------ позиция --------------------------------- */

// MoveNearTray ставит окно в правый нижний угол рабочей области —
// стандартное положение попапов трея (как системные уведомления Windows).
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

/* ------------------------------ скругления ------------------------------- */

// RoundCorners скругляет углы окна.
// Windows 11 — нативные скругления DWM (гладкие, переживают ресайз).
// Windows 10 — окно клипается регионом SetWindowRgn; край без сглаживания
// сглажен быть не может физически (GDI-регионы 1-битные), поэтому радиус
// региона выбран чуть меньше CSS — разрез проходит по тёмной hairline из CSS.
// Регион считается от фактического RECT: после смены режима (размера окна)
// пересчитывается; вызов идемпотентен.
func RoundCorners(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    if tryDWMCorners(hwnd) {
        return
    }
    applyRegion(hwnd)
    // WindowSetSize в Wails асинхронный: регион мог быть посчитан по старому
    // размеру. Пересчитываем ещё пару раз по актуальному RECT.
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
    return hr == 0 // S_OK только на Windows 11
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
    // Система забирает регион во владение — удалять его нельзя.
    procSetWindowRgn.Call(hwnd, rgn, 1)
}

/* ------------------------------- фокус ----------------------------------- */

// FocusWindow активирует окно (фокус + передний план).
func FocusWindow(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    procShowWindow.Call(hwnd, swRestore) // если окно минимизировано — развернуть
    procSetForegroundWindow.Call(hwnd)
}