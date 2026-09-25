//go:build windows

package tray

import (
	"encoding/binary"
	"log"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

/* Self-contained Win32 tray backend (Shell_NotifyIcon + a popup menu).
 *
// WHY a library was dropped for Windows: energye/systray v1.0.3 suffered a
// recurring, unreproducible-by-us failure — the icon stayed visible but
// stopped delivering clicks (verified: click callbacks never fired, logged
// as the first statement inside them), and its TaskbarCreated re-add never
// brought the icon back after an explorer.exe restart. Post-mortem dumps
// showed both message pumps alive and idle, zero deadlocks — i.e. the
// shell-to-window association broke inside the library's Windows layer.
// A tray widget must not have such a failure mode, so the Windows backend
// is owned here: every step is logged (add, TaskbarCreated, re-add, loop
// exit), and the message loop is the only thing running on the tray thread.
// Application logic is always dispatched to goroutines; the sole exception
// is the modal TrackPopupMenu, which must run on this thread (same as any
// Win32 menu). */

const (
    wmNull        = 0x0000
    wmDestroy     = 0x0002
    wmClose       = 0x0010
    wmContextMenu = 0x007B
    wmLButtonUp   = 0x0202
    wmLButtonDbl  = 0x0203
    wmRButtonUp   = 0x0205

    wmApp = 0x8000

    nimAdd     = 0
    nimModify  = 1
    nimDelete  = 2
    nifMessage = 0x00000001
    nifIcon    = 0x00000002
    nifTip     = 0x00000004

    mfChecked   = 0x00000008
    mfGrayed    = 0x00000001
    mfPopup     = 0x00000010
    mfString    = 0x00000000
    mfSeparator = 0x00000800

    miimState  = 0x00000001
    miimString = 0x00000040

    tpmRightButton = 0x0002
    tpmNoNotify    = 0x0080
    tpmReturnCmd   = 0x0100

    smCxIcon = 11
    smCyIcon = 12
)

var (
    user32   = windows.NewLazySystemDLL("user32.dll")
    shell32  = windows.NewLazySystemDLL("shell32.dll")
    kernel32 = windows.NewLazySystemDLL("kernel32.dll")

    procGetMessageW         = user32.NewProc("GetMessageW")
    procTranslateMessage    = user32.NewProc("TranslateMessage")
    procDispatchMessageW    = user32.NewProc("DispatchMessageW")
    procDefWindowProcW      = user32.NewProc("DefWindowProcW")
    procRegisterClassExW    = user32.NewProc("RegisterClassExW")
    procCreateWindowExW     = user32.NewProc("CreateWindowExW")
    procDestroyWindow       = user32.NewProc("DestroyWindow")
    procPostQuitMessage     = user32.NewProc("PostQuitMessage")
    procPostMessageW        = user32.NewProc("PostMessageW")
    procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
    procGetCursorPos        = user32.NewProc("GetCursorPos")
    procTrackPopupMenuEx    = user32.NewProc("TrackPopupMenuEx")
    procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
    procAppendMenuW         = user32.NewProc("AppendMenuW")
    procSetMenuItemInfoW    = user32.NewProc("SetMenuItemInfoW")
    procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
    procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
    procLookupIconID        = user32.NewProc("LookupIconIdFromDirectoryEx")
    procCreateIconFromRes   = user32.NewProc("CreateIconFromResourceEx")
    procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
    procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

type point struct{ X, Y int32 }

type msg struct {
    HWnd    uintptr
    Message uint32
    WParam  uintptr
    LParam  uintptr
    Time    uint32
    Pt      point
    Private uint32
}

type wndClassEx struct {
    CbSize, Style                uint32
    LpfnWndProc                  uintptr
    CbClsExtra, CbWndExtra       int32
    HInstance, HIcon, HCursor    uintptr
    HBrBackground, LpszMenuName  uintptr
    LpszClassName, HIconSm       uintptr
}

type notifyIconData struct {
    CbSize           uint32
    HWnd             uintptr
    UID, UFlags      uint32
    UCallbackMessage uint32
    HIcon            uintptr
    SzTip            [128]uint16
    DwState          uint32
    DwStateMask      uint32
    SzInfo           [256]uint16
    UVersion         uint32
    SzInfoTitle      [64]uint16
    DwInfoFlags      uint32
    GuidItem         [16]byte
    HBalloonIcon     uintptr
}

type menuItemInfo struct {
    CbSize, FMask, FType, FState, WID uint32
    HSubMenu                          uintptr
    HBmpChecked, HBmpUnchecked        uintptr
    DwTypeData                        uintptr
    Cch                               uint32
    HItem                             uintptr
}

// sysMenu is a handle to a tray menu item (or a submenu root).
type sysMenu struct {
    parentHmenu uintptr // the HMENU containing this item
    pos         int     // index within the parent (stable: append-only menu)
    selfHmenu   uintptr // submenu roots: their own HMENU
    id          uintptr // leaf command id (0 for separators/submenu roots)
    checked     bool
    dis         bool // grayed-out flag (named "dis": a field may not share a name with a method)
    clickFn     func()
}

var (
    natMu        sync.Mutex // guards every piece of native state below
    trayHwnd     uintptr
    trayHmenu    uintptr
    trayAdded    bool
    trayIconByt  []byte
    trayTip      string
    trayHicon    uintptr
    trayCbMsgID  uint32
    wmTaskbarMsg uint32
    classNamePtr uintptr
    trayExit     chan struct{}

    onClickFn, onRClickFn, onDClickFn func()

    cmdMap     = map[uintptr]*sysMenu{} // command id -> leaf item
    posCounter = map[uintptr]int{}      // HMENU -> next append position
)

var trayWndProcCB = syscall.NewCallback(trayWndProc)

func utf16p(s string) uintptr {
    p, err := windows.UTF16PtrFromString(s)
    if err != nil {
        return 0
    }
    return uintptr(unsafe.Pointer(p))
}

func nextPos(hmenu uintptr) int {
    p := posCounter[hmenu]
    posCounter[hmenu] = p + 1
    return p
}

/* ------------------------------ lifecycle -------------------------------- */

func sysRun(onReady func()) {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()

    hinst, _, _ := procGetModuleHandleW.Call(0)
    trayCbMsgID = wmApp + 1

    u16, _ := windows.UTF16FromString("CWidgetTray")
    classNamePtr = uintptr(unsafe.Pointer(&u16[0]))
    wmTaskbarMsg = registerWindowMessage("TaskbarCreated")

    var wc wndClassEx
    wc.CbSize = uint32(unsafe.Sizeof(wc))
    wc.LpfnWndProc = trayWndProcCB
    wc.HInstance = hinst
    wc.LpszClassName = classNamePtr
    if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
        log.Printf("[tray] RegisterClassExW failed")
        return
    }

    h, _, _ := procCreateWindowExW.Call(0, classNamePtr, classNamePtr, 0,
        0, 0, 0, 0, 0, 0, hinst, 0)
    if h == 0 {
        log.Printf("[tray] CreateWindowExW failed")
        return
    }
    // A normal (not message-only) top-level window: it must receive the
    // TaskbarCreated broadcast, which message-only windows never get.
    trayHwnd = h

    onReady() // builds the menu, seeds callbacks, stores icon bytes and tooltip

    if nativeNimAdd() {
        log.Printf("[tray] icon added")
    } else {
        log.Printf("[tray] failed to add the tray icon")
    }

    trayExit = make(chan struct{})
    defer close(trayExit)

    var m msg
    for {
        r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
        if r == 0 {
            break // WM_QUIT
        }
        procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
        procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
    }
    log.Printf("[tray] message loop exited")
    nativeNimDelete()
}

func sysQuit() {
    natMu.Lock()
    h := trayHwnd
    natMu.Unlock()
    if h != 0 {
        procPostMessageW.Call(h, wmClose, 0, 0)
    }
    // Wait briefly so the icon is removed before the process exits —
    // a race here would leave a stale icon until hover.
    select {
    case <-trayExit:
    case <-time.After(2 * time.Second):
        log.Printf("[tray] timed out waiting for the tray loop to exit")
    }
}

/* ------------------------- icon, tooltip, menu ---------------------------- */

func sysSetIcon(b []byte) {
    natMu.Lock()
    defer natMu.Unlock()
    trayIconByt = b
    trayHicon = 0 // recreated on the next add
    if !trayAdded {
        return
    }
    cx, _, _ := procGetSystemMetrics.Call(smCxIcon)
    cy, _, _ := procGetSystemMetrics.Call(smCyIcon)
    if h, ok := hiconFromICOBytes(b, int(cx), int(cy)); ok {
        trayHicon = h
        var nd notifyIconData
        nd.CbSize = uint32(unsafe.Sizeof(nd))
        nd.HWnd = trayHwnd
        nd.UID = 1
        nd.UFlags = nifIcon
        nd.HIcon = h
        procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nd)))
    }
}

func sysSetTooltip(s string) {
    natMu.Lock()
    defer natMu.Unlock()
    trayTip = s
    nativeNimModifyTip()
}

func sysSetOnClick(fn func())  { natMu.Lock(); onClickFn = fn; natMu.Unlock() }
func sysSetOnDClick(fn func()) { natMu.Lock(); onDClickFn = fn; natMu.Unlock() }

// sysSetOnRClick: fn runs synchronously on the tray thread (it only logs),
// the menu is displayed right after by this backend.
func sysSetOnRClick(fn func()) { natMu.Lock(); onRClickFn = fn; natMu.Unlock() }

func sysAddItem(title, tip string) *sysMenu {
    natMu.Lock()
    defer natMu.Unlock()
    id := nextCmdID()
    procAppendMenuW.Call(trayHmenu, mfString, id, utf16p(title))
    sm := &sysMenu{parentHmenu: trayHmenu, pos: nextPos(trayHmenu), id: id}
    cmdMap[id] = sm
    return sm
}

func sysAddCheckboxItem(title, tip string, checked bool) *sysMenu {
    natMu.Lock()
    defer natMu.Unlock()
    id := nextCmdID()
    flags := uintptr(mfString)
    if checked {
        flags |= mfChecked
    }
    procAppendMenuW.Call(trayHmenu, flags, id, utf16p(title))
    sm := &sysMenu{parentHmenu: trayHmenu, pos: nextPos(trayHmenu), id: id, checked: checked}
    cmdMap[id] = sm
    return sm
}

func sysAddSeparator() *sysMenu {
    natMu.Lock()
    defer natMu.Unlock()
    procAppendMenuW.Call(trayHmenu, mfSeparator, 0, 0)
    nextPos(trayHmenu)
    return &sysMenu{parentHmenu: trayHmenu, pos: -1}
}

// sysAddSubmenuItem creates a submenu root at the top level.
func sysAddSubmenuItem(title, tip string) *sysMenu {
    natMu.Lock()
    defer natMu.Unlock()
    hsub, _, _ := procCreatePopupMenu.Call()
    procAppendMenuW.Call(trayHmenu, mfPopup|mfString, hsub, utf16p(title))
    return &sysMenu{parentHmenu: trayHmenu, pos: nextPos(trayHmenu), selfHmenu: hsub}
}

func (m *sysMenu) addSubItem(title, tip string) *sysMenu {
    if m == nil || m.selfHmenu == 0 {
        return &sysMenu{}
    }
    natMu.Lock()
    defer natMu.Unlock()
    id := nextCmdID()
    procAppendMenuW.Call(m.selfHmenu, mfString, id, utf16p(title))
    sm := &sysMenu{parentHmenu: m.selfHmenu, pos: nextPos(m.selfHmenu), id: id}
    cmdMap[id] = sm
    return sm
}

func (m *sysMenu) setTitle(s string) {
    if m == nil || m.pos < 0 {
        return
    }
    natMu.Lock()
    defer natMu.Unlock()
    t, _ := windows.UTF16FromString(s)
    var mi menuItemInfo
    mi.CbSize = uint32(unsafe.Sizeof(mi))
    mi.FMask = miimString
    mi.DwTypeData = uintptr(unsafe.Pointer(&t[0]))
    procSetMenuItemInfoW.Call(m.parentHmenu, uintptr(m.pos), 1, uintptr(unsafe.Pointer(&mi)))
}

func (m *sysMenu) setTooltip(string) {} // Win32 menus have no per-item tooltips

func (m *sysMenu) applyState() {
    if m == nil || m.pos < 0 {
        return
    }
    var mi menuItemInfo
    mi.CbSize = uint32(unsafe.Sizeof(mi))
    mi.FMask = miimState
    if m.checked {
        mi.FState |= mfChecked
    }
    if m.dis{
        mi.FState |= mfGrayed
    }
    procSetMenuItemInfoW.Call(m.parentHmenu, uintptr(m.pos), 1, uintptr(unsafe.Pointer(&mi)))
}

func (m *sysMenu) enable() {
    if m == nil {
        return
    }
    natMu.Lock()
    m.dis = false
    natMu.Unlock()
    m.applyState()
}

func (m *sysMenu) disable() {
    if m == nil {
        return
    }
    natMu.Lock()
    m.dis = true
    natMu.Unlock()
    m.applyState()
}

func (m *sysMenu) setChecked(b bool) {
    if m == nil {
        return
    }
    natMu.Lock()
    m.checked = b
    natMu.Unlock()
    m.applyState()
}

func (m *sysMenu) disabled() bool {
    if m == nil {
        return false
    }
    natMu.Lock()
    defer natMu.Unlock()
    return m.dis
}

func (m *sysMenu) onClick(fn func()) {
    if m == nil {
        return
    }
    natMu.Lock()
    m.clickFn = fn
    natMu.Unlock()
}

/* ------------------------------ internals --------------------------------- */

func nextCmdID() uintptr {
    id := posCounter[0] // the command-id counter is stored under key 0
    posCounter[0] = id + 1
    if id == 0 { // 0 means "dismissed" for TPM_RETURNCMD
        id = 1
        posCounter[0] = 2
    }
    return uintptr(id)
}

func registerWindowMessage(name string) uint32 {
    p, err := windows.UTF16PtrFromString(name)
    if err != nil {
        return 0
    }
    r, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(p)))
    return uint32(r)
}

func hiconFromICOBytes(b []byte, cx, cy int) (uintptr, bool) {
    if len(b) < 22 {
        return 0, false
    }
    off, _, _ := procLookupIconID.Call(uintptr(unsafe.Pointer(&b[0])), 1, uintptr(cx), uintptr(cy), 0)
    if off == 0 {
        return 0, false
    }
    offInt := int(off)
    count := int(binary.LittleEndian.Uint16(b[4:6]))
    size := 0
    for i := 0; i < count; i++ {
        e := 6 + i*16
        if e+16 > len(b) {
            break
        }
        if int(binary.LittleEndian.Uint32(b[e+12:e+16])) == offInt {
            size = int(binary.LittleEndian.Uint32(b[e+8 : e+12]))
            break
        }
    }
    if size == 0 || offInt+size > len(b) {
        return 0, false
    }
    h, _, _ := procCreateIconFromRes.Call(uintptr(unsafe.Pointer(&b[offInt])),
        uintptr(size), 1, 0x00030000, uintptr(cx), uintptr(cy), 0)
    return h, h != 0
}

func nativeNimAdd() bool {
    if trayHwnd == 0 {
        return false
    }
    if trayHicon == 0 && len(trayIconByt) > 0 {
        cx, _, _ := procGetSystemMetrics.Call(smCxIcon)
        cy, _, _ := procGetSystemMetrics.Call(smCyIcon)
        if h, ok := hiconFromICOBytes(trayIconByt, int(cx), int(cy)); ok {
            trayHicon = h
        }
    }
    var nd notifyIconData
    nd.CbSize = uint32(unsafe.Sizeof(nd))
    nd.HWnd = trayHwnd
    nd.UID = 1
    nd.UFlags = nifMessage | nifTip
    if trayHicon != 0 {
        nd.UFlags |= nifIcon
        nd.HIcon = trayHicon
    }
    nd.UCallbackMessage = trayCbMsgID
    tip, _ := windows.UTF16FromString(trayTip)
    if len(tip) > 127 {
        tip = tip[:127]
    }
    copy(nd.SzTip[:], tip)
    r, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nd)))
    trayAdded = r != 0
    return trayAdded
}

func nativeNimModifyTip() {
    if !trayAdded {
        return
    }
    var nd notifyIconData
    nd.CbSize = uint32(unsafe.Sizeof(nd))
    nd.HWnd = trayHwnd
    nd.UID = 1
    nd.UFlags = nifTip
    tip, _ := windows.UTF16FromString(trayTip)
    if len(tip) > 127 {
        tip = tip[:127]
    }
    copy(nd.SzTip[:], tip)
    procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nd)))
}

func nativeNimDelete() {
    if !trayAdded {
        return
    }
    var nd notifyIconData
    nd.CbSize = uint32(unsafe.Sizeof(nd))
    nd.HWnd = trayHwnd
    nd.UID = 1
    procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nd)))
    trayAdded = false
}

func nativeReAdd() {
    natMu.Lock()
    nativeNimDelete()
    ok := nativeNimAdd()
    natMu.Unlock()
    if ok {
        return
    }
    go func() { // a stubborn shell may need a moment; retry with a backoff
        for i := 1; i <= 5; i++ {
            time.Sleep(time.Duration(500*i) * time.Millisecond)
            natMu.Lock()
            ok := nativeNimAdd()
            natMu.Unlock()
            if ok {
                log.Printf("[tray] icon re-added on attempt %d", i)
                return
            }
        }
        log.Printf("[tray] failed to re-add the icon after TaskbarCreated")
    }()
}

func nativeShowMenu(hwnd uintptr) {
    natMu.Lock()
    hm := trayHmenu
    fn := onRClickFn
    natMu.Unlock()
    if hm == 0 {
        return // the menu is not built yet
    }
    if fn != nil {
        fn() // synchronous: logging precedes the modal loop
    }
    procSetForegroundWindow.Call(hwnd) // classic requirement for tray menus
    var pt point
    procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
    cmd, _, _ := procTrackPopupMenuEx.Call(hm,
        tpmReturnCmd|tpmRightButton|tpmNoNotify,
        uintptr(pt.X), uintptr(pt.Y), hwnd, 0)
    procPostMessageW.Call(hwnd, wmNull, 0, 0) // dismiss helper (KB135788)
    if cmd == 0 {
        return // dismissed
    }
    natMu.Lock()
    sm := cmdMap[cmd]
    natMu.Unlock()
    if sm != nil && sm.clickFn != nil {
        go sm.clickFn()
    }
}

func trayWndProc(hwnd, m, wp, lp uintptr) uintptr {
    mid := uint32(m)
    switch {
    case mid == trayCbMsgID: // shell icon callback: lp = the mouse message
        switch uint32(lp) {
        case wmLButtonUp:
            natMu.Lock()
            fn := onClickFn
            natMu.Unlock()
            if fn != nil {
                go fn()
            }
        case wmLButtonDbl:
            natMu.Lock()
            fn := onDClickFn
            natMu.Unlock()
            if fn != nil {
                go fn()
            }
        case wmRButtonUp, wmContextMenu:
            nativeShowMenu(hwnd)
        }
        return 0
    case mid == wmContextMenu: // keyboard-invoked menu (Shift+F10)
        nativeShowMenu(hwnd)
        return 0
    case mid == wmTaskbarMsg:
        // Explorer restarted (or the taskbar was recreated): the shell
        // destroyed every tray icon. This is the exact path the library
        // silently failed on — here it is explicit and logged.
        log.Printf("[tray] TaskbarCreated received — re-adding the icon")
        nativeReAdd()
        return 0
    case m == wmClose:
        nativeNimDelete()
        procDestroyWindow.Call(hwnd)
        return 0
    case m == wmDestroy:
        procPostQuitMessage.Call(0)
        return 0
    }
    r, _, _ := procDefWindowProcW.Call(hwnd, m, wp, lp)
    return r
}