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

/* Self-contained Win32 tray backend: Shell_NotifyIcon + TrackPopupMenu.
 *
// WHY a hand-rolled backend instead of a library: energye/systray's Windows
// layer had a recurring field failure (three incidents, one identical dump
// signature): the icon stayed visible but stopped delivering clicks, and
// after an explorer.exe restart the icon never came back. Post-mortem dumps
// showed both message pumps alive and idle — the shell↔window association
// broke inside the library, invisible and unloggable from outside. This file
// owns the whole Windows layer: every step is logged, so a future failure
// names itself.
//
// Threading: the window + message loop live on one locked OS thread (run).
// Click callbacks are dispatched to goroutines by the logic layer (safeGo).
// Menu/icon mutations may come from any goroutine and are serialized by
// b.mu; TrackPopupMenu is modal but runs WITHOUT the mutex (a modal loop
// must not block the thread's own wndProc) — the command-id -> handler map
// is snapshotted under the mutex before the modal call instead. */

const (
    wmAppTrayCb = 0x8000 // WM_APP-base callback id for the icon

    wmClose         = 0x0010
    wmContextMenu   = 0x007B // keyboard-invoked menu (Shift+F10)
    wmDestroy       = 0x0002
    wmNull          = 0x0000
    wmLButtonUp     = 0x0202
    wmLButtonDblClk = 0x0203
    wmRButtonUp     = 0x0205

    nimAdd    = 0
    nimModify = 1
    nimDelete = 2

    nifMessage = 0x00000001
    nifIcon    = 0x00000002
    nifTip     = 0x00000004

    mfString    = 0x00000000
    mfChecked   = 0x00000008
    mfPopup     = 0x00000010
    mfSeparator = 0x00000800
    mfGrayed    = 0x00000001

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
    procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
    procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
    procAppendMenuW         = user32.NewProc("AppendMenuW")
    procSetMenuItemInfoW    = user32.NewProc("SetMenuItemInfoW")
    procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
    procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
    procCreateIconFromResEx = user32.NewProc("CreateIconFromResourceEx")
    procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
    procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

type w32Point struct{ X, Y int32 }

type w32Msg struct {
    HWnd    uintptr
    Message uint32
    WParam  uintptr
    LParam  uintptr
    Time    uint32
    Pt      w32Point
}

type w32WndClassEx struct {
    CbSize        uint32
    Style         uint32
    LpfnWndProc   uintptr
    CbClsExtra    int32
    CbWndExtra    int32
    HInstance     uintptr
    HIcon         uintptr
    HCursor       uintptr
    HbrBackground uintptr
    LpszMenuName  uintptr
    LpszClassName uintptr
    HIconSm       uintptr
}

type w32NotifyIconData struct {
    CbSize           uint32
    HWnd             uintptr
    UID              uint32
    UFlags           uint32
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

// w32MenuItemInfo mirrors MENUITEMINFOW EXACTLY, including ULONG_PTR
// dwItemData between hbmpUnchecked and dwTypeData. The earlier version of
// this struct was missing that field: the layout shifted by 8 bytes,
// cbSize mismatched sizeof(MENUITEMINFO), and every SetMenuItemInfoW call
// wrote an empty/garbage text — items rendered as thin empty lines that
// looked exactly like separators (the "separator instead of Refresh" and
// "language change breaks the menu" symptoms).
type w32MenuItemInfo struct {
    CbSize        uint32
    FMask         uint32
    FType         uint32
    FState        uint32
    WID           uint32
    HSubMenu      uintptr
    HBmpChecked   uintptr
    HBmpUnchecked uintptr
    DwItemData    uintptr // ← the field whose absence broke every setTitle/applyState
    DwTypeData    uintptr
    Cch           uint32
    HItem         uintptr
}

// win32Backend implements the backend contract (backend.go) natively.
type win32Backend struct {
    iconICO []byte

    mu       sync.Mutex
    hwnd     uintptr
    hmenu    uintptr
    hicon    uintptr
    added    bool
    tip      string
    items    map[uintptr]*win32Item // command id -> leaf item
    childPos map[uintptr]int        // HMENU -> next append position
    nextCmd  uintptr
    quitCh   chan struct{}
    quitOnce sync.Once

    onClickFn  func()
    onDClickFn func()
    onRClickFn func()
}

// win32Item is a handle to one menu item.
type win32Item struct {
    be       *win32Backend
    parent   uintptr // HMENU containing this item
    pos      int     // position within the parent (menus are append-only)
    hsubmenu uintptr // submenu roots: their own HMENU
    id       uintptr // leaf command id (0 = separator/submenu root)
    checked  bool
    dis      bool
    clickFn  func()
}

// One tray per process: the wndProc callback reaches the backend through
// this package-level reference.
var (
    activeWin32      *win32Backend
    trayWndProcCB    = syscall.NewCallback(trayWndProc)
    wmTaskbarCreated uint32
)

func newBackend(iconICO, iconPNG []byte) backend {
    return &win32Backend{
        iconICO:  iconICO,                    // Windows: .ico; the PNG is ignored
        items:    map[uintptr]*win32Item{},   // nil maps would panic on the first write
        childPos: map[uintptr]int{},
    }
}

/* ------------------------------ lifecycle -------------------------------- */

func (b *win32Backend) run(onReady func()) {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()

    activeWin32 = b
    b.quitCh = make(chan struct{})
    wmTaskbarCreated = registerWindowMessage("TaskbarCreated")

    hinst, _, _ := procGetModuleHandleW.Call(0)
    cls := utf16Slice("CWidgetTrayWnd")
    var wc w32WndClassEx
    wc.CbSize = uint32(unsafe.Sizeof(wc))
    wc.LpfnWndProc = trayWndProcCB
    wc.HInstance = hinst
    wc.LpszClassName = uintptr(unsafe.Pointer(&cls[0]))
    if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
        log.Printf("[tray] RegisterClassExW failed — tray disabled")
        close(b.quitCh)
        return
    }
    // A normal top-level hidden window: a message-only window would never
    // receive the TaskbarCreated broadcast, which we must handle.
    hwnd, _, _ := procCreateWindowExW.Call(0,
        wc.LpszClassName, wc.LpszClassName, 0,
        0, 0, 0, 0, 0, 0, hinst, 0)
    if hwnd == 0 {
        log.Printf("[tray] CreateWindowExW failed — tray disabled")
        close(b.quitCh)
        return
    }
    b.mu.Lock()
    b.hwnd = hwnd
    b.hmenu, _, _ = procCreatePopupMenu.Call() // the menu ROOT; items append into it
    if b.hmenu == 0 {
        b.mu.Unlock()
        log.Printf("[tray] CreatePopupMenu failed — tray disabled")
        procDestroyWindow.Call(hwnd)
        close(b.quitCh)
        return
    }
    b.mu.Unlock()

    onReady() // the menu is built here, on the tray thread, before the loop

    // Decisive diagnostics: a non-zero item count with a still-missing menu
    // would point at TrackPopupMenu; zero items — at AppendMenu failures.
    b.mu.Lock()
    n := len(b.items)
    hm := b.hmenu
    b.mu.Unlock()
    log.Printf("[tray] menu built (%d items, root=0x%x)", n, hm)

    if b.nimAdd() {
        log.Printf("[tray] icon added")
    } else {
        log.Printf("[tray] Shell_NotifyIcon(NIM_ADD) failed")
    }

    var m w32Msg
    for {
        r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
        if r == 0 || r == ^uintptr(0) {
            break // WM_QUIT or GetMessage error
        }
        procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
        procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
    }
    log.Printf("[tray] message loop exited")
    b.mu.Lock()
    b.nimDelete()
    b.mu.Unlock()
    b.quitOnce.Do(func() { close(b.quitCh) })
}

func (b *win32Backend) quit() {
    b.quitOnce.Do(func() {
        go func() {
            b.mu.Lock()
            hwnd := b.hwnd
            b.mu.Unlock()
            if hwnd != 0 {
                procPostMessageW.Call(hwnd, wmClose, 0, 0)
            }
        }()
    })
    // Wait: the icon must be removed before the process exits, or a stale
    // icon stays in the tray until hover.
    select {
    case <-b.quitCh:
    case <-time.After(3 * time.Second):
        log.Printf("[tray] timed out waiting for the tray loop to exit")
    }
}

/* --------------------------- backend contract ----------------------------- */

func (b *win32Backend) setTooltip(s string) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.tip = s
    b.nimModifyTipLocked()
}

func (b *win32Backend) setOnClick(fn func())  { b.mu.Lock(); b.onClickFn = fn; b.mu.Unlock() }
func (b *win32Backend) setOnDClick(fn func()) { b.mu.Lock(); b.onDClickFn = fn; b.mu.Unlock() }
func (b *win32Backend) setOnRClick(fn func()) { b.mu.Lock(); b.onRClickFn = fn; b.mu.Unlock() }

// Locked read accessors for trayWndProc: the callback fires on the tray
// thread, the handlers themselves are dispatched to the logic layer
// (safeGo in tray.go), but reading the fields must still be serialized.
func (b *win32Backend) getOnClick() func() {
    b.mu.Lock()
    defer b.mu.Unlock()
    return b.onClickFn
}

func (b *win32Backend) getDClick() func() {
    b.mu.Lock()
    defer b.mu.Unlock()
    return b.onDClickFn
}

func (b *win32Backend) addItem(title, tip string) menuHandle {
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.hmenu == 0 {
        log.Printf("[tray] addItem %q skipped: menu not created", title)
        return &win32Item{}
    }
    id := b.nextCmdIDLocked()
    u := utf16Slice(title)
    if r, _, _ := procAppendMenuW.Call(b.hmenu, mfString, id, uintptr(unsafe.Pointer(&u[0]))); r == 0 {
        log.Printf("[tray] AppendMenuW failed for %q — item skipped", title)
        return &win32Item{} // no-op: all methods guard on zero fields
    }
    // The position counter is bumped ONLY after a successful append: it is
    // the mirror of the real menu, and SetMenuItemInfoW addresses by it.
    it := &win32Item{be: b, parent: b.hmenu, pos: b.appendPosLocked(b.hmenu), id: id}
    b.items[id] = it
    return it
}

func (b *win32Backend) addCheckboxItem(title, tip string, checked bool) menuHandle {
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.hmenu == 0 {
        log.Printf("[tray] addCheckboxItem %q skipped: menu not created", title)
        return &win32Item{}
    }
    id := b.nextCmdIDLocked()
    flags := uintptr(mfString)
    if checked {
        flags |= mfChecked
    }
    u := utf16Slice(title)
    if r, _, _ := procAppendMenuW.Call(b.hmenu, flags, id, uintptr(unsafe.Pointer(&u[0]))); r == 0 {
        log.Printf("[tray] AppendMenuW failed for %q — item skipped", title)
        return &win32Item{}
    }
    it := &win32Item{be: b, parent: b.hmenu, pos: b.appendPosLocked(b.hmenu), id: id, checked: checked}
    b.items[id] = it
    return it
}

func (b *win32Backend) addSeparator() {
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.hmenu == 0 {
        return
    }
    // A separator occupies a menu position like any entry — the counter is
    // bumped only after a successful append (same rule as the items).
    if r, _, _ := procAppendMenuW.Call(b.hmenu, mfSeparator, 0, 0); r == 0 {
        log.Printf("[tray] AppendMenuW failed for a separator")
        return
    }
    b.appendPosLocked(b.hmenu)
}

func (b *win32Backend) addSubmenuItem(title, tip string) menuHandle {
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.hmenu == 0 {
        log.Printf("[tray] addSubmenuItem %q skipped: menu not created", title)
        return &win32Item{}
    }
    hsub, _, _ := procCreatePopupMenu.Call()
    if hsub == 0 {
        log.Printf("[tray] CreatePopupMenu failed for the %q submenu", title)
        return &win32Item{}
    }
    // A submenu root takes MF_POPUP and the HSUBMENU as the item value —
    // there is NO command id here (that is why the logging template with
    // "id" did not fit this function), and it is not clickable itself.
    u := utf16Slice(title)
    if r, _, _ := procAppendMenuW.Call(b.hmenu, mfPopup|mfString, hsub, uintptr(unsafe.Pointer(&u[0]))); r == 0 {
        log.Printf("[tray] AppendMenuW failed for the %q submenu", title)
        return &win32Item{}
    }
    return &win32Item{be: b, parent: b.hmenu, pos: b.appendPosLocked(b.hmenu), hsubmenu: hsub}
}

/* ----------------------------- menu handle -------------------------------- */

func (i *win32Item) addSubItem(title, tip string) menuHandle {
    if i == nil || i.hsubmenu == 0 {
        return &win32Item{} // no-op: subitems are added to submenu roots only
    }
    i.be.mu.Lock()
    defer i.be.mu.Unlock()
    id := i.be.nextCmdIDLocked()
    u := utf16Slice(title)
    if r, _, _ := procAppendMenuW.Call(i.hsubmenu, mfString, id, uintptr(unsafe.Pointer(&u[0]))); r == 0 {
        log.Printf("[tray] AppendMenuW failed for %q — item skipped", title)
        return &win32Item{}
    }
    it := &win32Item{be: i.be, parent: i.hsubmenu, pos: i.be.appendPosLocked(i.hsubmenu), id: id}
    i.be.items[id] = it
    return it
}

func (i *win32Item) setTitle(s string) {
    if i == nil || i.pos < 0 {
        return
    }
    i.be.mu.Lock()
    defer i.be.mu.Unlock()
    u := utf16Slice(s)
    var mi w32MenuItemInfo
    mi.CbSize = uint32(unsafe.Sizeof(mi))
    mi.FMask = miimString
    mi.DwTypeData = uintptr(unsafe.Pointer(&u[0]))
    if r, _, e := procSetMenuItemInfoW.Call(i.parent, uintptr(i.pos), 1, uintptr(unsafe.Pointer(&mi))); r == 0 {
        log.Printf("[tray] SetMenuItemInfoW(title) failed at pos %d: %v", i.pos, e)
    }
    runtime.KeepAlive(u) // the string must survive the syscall (passed via a struct field)
}

func (i *win32Item) setTooltip(string) {} // Win32 menus have no per-item tooltips

func (i *win32Item) enable() {
    if i == nil {
        return
    }
    i.be.mu.Lock()
    i.dis = false
    i.be.mu.Unlock()
    i.applyState()
}

func (i *win32Item) disable() {
    if i == nil {
        return
    }
    i.be.mu.Lock()
    i.dis = true
    i.be.mu.Unlock()
    i.applyState()
}

func (i *win32Item) setChecked(b bool) {
    if i == nil {
        return
    }
    i.be.mu.Lock()
    i.checked = b
    i.be.mu.Unlock()
    i.applyState()
}

func (i *win32Item) disabled() bool {
    if i == nil {
        return false
    }
    i.be.mu.Lock()
    defer i.be.mu.Unlock()
    return i.dis
}

func (i *win32Item) onClick(fn func()) {
    if i == nil || i.id == 0 {
        return // separators and submenu roots are not clickable
    }
    i.be.mu.Lock()
    i.clickFn = fn
    i.be.mu.Unlock()
}

// Caller holds the backend lock.
func (i *win32Item) applyState() {
    if i == nil || i.pos < 0 {
        return
    }
    var mi w32MenuItemInfo
    mi.CbSize = uint32(unsafe.Sizeof(mi))
    mi.FMask = miimState
    if i.checked {
        mi.FState |= mfChecked
    }
    if i.dis {
        mi.FState |= mfGrayed
    }
    if r, _, e := procSetMenuItemInfoW.Call(i.parent, uintptr(i.pos), 1, uintptr(unsafe.Pointer(&mi))); r == 0 {
        log.Printf("[tray] SetMenuItemInfoW(state) failed at pos %d: %v", i.pos, e)
    }
}

/* ------------------------------- internals -------------------------------- */

// Caller holds the backend lock.
func (b *win32Backend) nextCmdIDLocked() uintptr {
    b.nextCmd++
    if b.nextCmd == 0 { // wrapped: practically impossible, stay safe
        b.nextCmd = 1 // 0 means "dismissed" for TPM_RETURNCMD
    }
    return b.nextCmd
}

// Caller holds the backend lock.
func (b *win32Backend) appendPosLocked(hmenu uintptr) int {
    p := b.childPos[hmenu]
    b.childPos[hmenu] = p + 1
    return p
}

// Caller holds the backend lock.
func (b *win32Backend) nimAdd() bool {
    if b.hwnd == 0 {
        return false
    }
    if b.hicon == 0 && len(b.iconICO) > 0 {
        cx, _, _ := procGetSystemMetrics.Call(smCxIcon)
        cy, _, _ := procGetSystemMetrics.Call(smCyIcon)
        b.hicon = hiconFromICOBytes(b.iconICO, int(cx), int(cy))
        if b.hicon != 0 {
            log.Printf("[tray] icon loaded from ICO bytes")
        } else {
            log.Printf("[tray] WARNING: could not load the icon — adding without one")
        }
    }
    var nd w32NotifyIconData
    nd.CbSize = uint32(unsafe.Sizeof(nd))
    nd.HWnd = b.hwnd
    nd.UID = 1
    nd.UFlags = nifMessage | nifTip
    if b.hicon != 0 {
        nd.UFlags |= nifIcon
        nd.HIcon = b.hicon
    }
    nd.UCallbackMessage = wmAppTrayCb
    copy(nd.SzTip[:], utf16Clamp(b.tip))
    r, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nd)))
    b.added = r != 0
    return b.added
}

// Caller holds the backend lock.
func (b *win32Backend) nimModifyTipLocked() {
    if !b.added {
        return
    }
    var nd w32NotifyIconData
    nd.CbSize = uint32(unsafe.Sizeof(nd))
    nd.HWnd = b.hwnd
    nd.UID = 1
    nd.UFlags = nifTip
    copy(nd.SzTip[:], utf16Clamp(b.tip))
    procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nd)))
}

// Caller holds the backend lock.
func (b *win32Backend) nimDelete() {
    if !b.added {
        return
    }
    var nd w32NotifyIconData
    nd.CbSize = uint32(unsafe.Sizeof(nd))
    nd.HWnd = b.hwnd
    nd.UID = 1
    procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nd)))
    b.added = false
}

func (b *win32Backend) reAdd() {
    for i := 1; i <= 5; i++ {
        b.mu.Lock()
        b.nimDelete()
        ok := b.nimAdd()
        b.mu.Unlock()
        if ok {
            log.Printf("[tray] icon re-added (attempt %d)", i)
            return
        }
        time.Sleep(time.Duration(500*i) * time.Millisecond)
    }
    log.Printf("[tray] failed to re-add the icon after TaskbarCreated")
}

// showMenu runs on the tray thread. The menu is shown WITHOUT holding the
// backend mutex: TrackPopupMenu runs a modal message loop on this very
// thread, and holding b.mu across it would deadlock — the loop needs the
// same thread's wndProc to be free to receive clicks. Sequence: snapshot
// the state under the mutex, run the modal menu on the free window
// (KB135788 dance), dispatch the chosen handler outside the lock.
func (b *win32Backend) showMenu() {
    b.mu.Lock()
    hmenu := b.hmenu
    hwnd := b.hwnd
    rFn := b.onRClickFn
    handlers := make(map[uintptr]func(), len(b.items))
    for id, it := range b.items {
        if it.clickFn != nil {
            handlers[id] = it.clickFn
        }
    }
    b.mu.Unlock()

    if hmenu == 0 || hwnd == 0 {
        log.Printf("[tray] showMenu skipped: menu or window not ready")
        return
    }
    if rFn != nil {
        rFn() // logging precedes the modal loop
    }
    // KB135788: SetForegroundWindow before TrackPopupMenu and a posted
    // WM_NULL after — otherwise a menu opened from a notification icon
    // refuses to dismiss on an outside click.
    procSetForegroundWindow.Call(hwnd)
    var pt w32Point
    procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
    cmd, _, callErr := procTrackPopupMenu.Call(
        hmenu, // the menu handle itself (it was missing once — the menu silently did not open)
        tpmReturnCmd|tpmRightButton|tpmNoNotify,
        uintptr(pt.X), uintptr(pt.Y),
        0,    // nReserved
        hwnd, // hWnd owning the menu
        0)    // prcRect = NULL
    procPostMessageW.Call(hwnd, wmNull, 0, 0)

    if eno, _ := callErr.(syscall.Errno); eno != 0 {
        log.Printf("[tray] TrackPopupMenu failed: %v", eno)
        return
    }
    if cmd == 0 {
        return // shown and dismissed without a selection
    }
    if fn := handlers[cmd]; fn != nil {
        go fn() // dispatched; the logic layer wraps in safeGo
    }
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
    b := activeWin32
    if b == nil {
        r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
        return r
    }
    switch uint32(msg) {
    case wmAppTrayCb: // shell icon callback: lparam = the mouse message
        switch lparam {
        case wmLButtonUp:
            if fn := b.getOnClick(); fn != nil {
                fn()
            }
        case wmLButtonDblClk:
            if fn := b.getDClick(); fn != nil {
                fn()
            }
        case wmRButtonUp, wmContextMenu:
            b.showMenu()
        }
        return 0
    case wmTaskbarCreated:
        // Explorer restarted (or the taskbar was recreated): the shell
        // destroyed every tray icon. This is the exact path the library
        // failed silently — here it is explicit, logged, and retried.
        log.Printf("[tray] TaskbarCreated received — re-adding the icon")
        go b.reAdd()
        return 0
    case wmClose:
        // Just destroy the window: WM_DESTROY posts WM_QUIT, the loop exits,
        // and run()'s cleanup path removes the icon under the lock.
        procDestroyWindow.Call(hwnd)
        return 0
    case wmDestroy:
        procPostQuitMessage.Call(0)
        return 0
    }
    r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
    return r
}

/* ------------------------------- helpers ---------------------------------- */

func registerWindowMessage(name string) uint32 {
    u, err := windows.UTF16FromString(name)
    if err != nil || len(u) == 0 {
        return 0
    }
    r, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(&u[0])))
    return uint32(r)
}

func utf16Slice(s string) []uint16 {
    u, err := windows.UTF16FromString(s)
    if err != nil || len(u) == 0 {
        return []uint16{0}
    }
    return u
}

// utf16Clamp fits s into the NOTIFYICONDATA tooltip buffer.
func utf16Clamp(s string) []uint16 {
    u := utf16Slice(s)
    if len(u) > 127 {
        u = u[:127]
    }
    return u
}

func absDiff(a, b int) int {
    if a > b {
        return a - b
    }
    return b - a
}

// hiconFromICOBytes picks the best-fitting entry of the ICO directory and
// creates an HICON from it. Returns 0 on any failure (the icon is then
// added without one — the tray still works, just iconless, and the log
// says so).
func hiconFromICOBytes(b []byte, cx, cy int) uintptr {
    if len(b) < 6 {
        return 0
    }
    count := int(binary.LittleEndian.Uint16(b[4:6]))
    best, bestDist := -1, 0
    for i := 0; i < count; i++ {
        e := 6 + i*16
        if e+16 > len(b) {
            break
        }
        w, h := int(b[e]), int(b[e+1])
        if w == 0 {
            w = 256
        }
        if h == 0 {
            h = 256
        }
        d := absDiff(w, cx) + absDiff(h, cy)
        if best == -1 || d < bestDist {
            best, bestDist = e, d
        }
    }
    if best < 0 {
        return 0
    }
    size := int(binary.LittleEndian.Uint32(b[best+8 : best+12]))
    off := int(binary.LittleEndian.Uint32(b[best+12 : best+16]))
    if size <= 0 || off <= 0 || off+size > len(b) {
        return 0
    }
    h, _, _ := procCreateIconFromResEx.Call(
        uintptr(unsafe.Pointer(&b[off])), uintptr(size), 1,
        0x00030000, uintptr(cx), uintptr(cy), 0)
    return h
}