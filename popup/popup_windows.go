//go:build windows

package popup

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
    user32                   = windows.NewLazySystemDLL("user32.dll")
    procFindWindow           = user32.NewProc("FindWindowW")
    procGetWindowLongW       = user32.NewProc("GetWindowLongW")
    procSetWindowLongW       = user32.NewProc("SetWindowLongW")
    procGetWindowRect        = user32.NewProc("GetWindowRect")
    procSetWindowPos         = user32.NewProc("SetWindowPos")
    procSystemParametersInfo = user32.NewProc("SystemParametersInfoW")
    procShowWindow           = user32.NewProc("ShowWindow")
    procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
)

const (
    // GWL_EXSTYLE = -20; в беззнаковом 32-битном виде это 0xFFFFFFEC.
    gwlExStyle     uintptr = 0xFFFFFFEC
    wsExToolWindow         = 0x00000080 // убирает кнопку из панели задач и Alt+Tab
    wsExAppWindow          = 0x00040000 // принудительная кнопка — снимаем её

    swpNoSize      = 0x0001
    swpNoZOrder    = 0x0004
    swpNoActivate  = 0x0010
    spiGetWorkArea = 0x0030
    swRestore      = 9

    trayMargin = 12 // отступ от краёв рабочей области, px
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

// MoveNearTray ставит окно в правый нижний угол рабочей области —
// стандартное положение попапов трея (как системные уведомления Windows).
// Рабочая область (SPI_GETWORKAREA) уже исключает панель задач,
// поэтому окно всегда оказывается НАД панелью при любом её расположении.
func MoveNearTray(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    var wr rect
    procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
    if wr.Right <= wr.Left || wr.Bottom <= wr.Top {
        return
    }
    var wa rect
    procSystemParametersInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
    if wa.Right <= wa.Left || wa.Bottom <= wa.Top {
        return
    }
    x := wa.Right - (wr.Right-wr.Left) - trayMargin
    y := wa.Bottom - (wr.Bottom-wr.Top) - trayMargin
    procSetWindowPos.Call(hwnd,
        0, // HWND_NOTOPMOST
        uintptr(x), uintptr(y), 0, 0,
        swpNoSize|swpNoZOrder|swpNoActivate)
}

// FocusWindow активирует окно (фокус + передний план).
// В Wails v2 аналога нет (WindowFocus появился только в v3), поэтому — Win32.
// Best-effort: Windows ограничивает передачу фокуса «чужим» процессам, но
// процесс, владеющий иконкой трея, по которой только что кликнул пользователь,
// имеет на это право.
func FocusWindow(title string) {
    hwnd := findHwnd(title)
    if hwnd == 0 {
        return
    }
    procShowWindow.Call(hwnd, swRestore) // если окно минимизировано — развернуть
    procSetForegroundWindow.Call(hwnd)
}