package tray

// The tray package is deliberately split into two layers:
//
//   - tray.go — the LOGIC layer: menu build order, checkbox seeding from the
//     sources of truth, cooldowns, degraded/offline tooltips, i18n
//     retranslation, panic-shielded dispatch. It knows nothing about any
//     tray library and talks only to the contracts below.
//   - backend_systray_windows.go / backend_systray_linux.go — the
//     IMPLEMENTATION layer: thin wrappers over github.com/energye/systray,
//     selected by build tags, encapsulating per-OS quirks (icon format,
//     which click shows the menu). Swapping the library — or moving Windows
//     to a self-contained Win32 backend — is rewriting ONE file.
//
// This file is the contract between the layers.

// backend is the implementation contract for the icon + menu.
//
// Contract rules for implementations:
//   - run blocks the calling goroutine until quit; onReady is invoked once
//     on the implementation's tray thread BEFORE the loop starts — all menu
//     building must happen inside it;
//   - click callbacks may fire on any implementation thread; the logic
//     layer never does heavy work inside them (see safeGo in tray.go);
//   - setOnRClick: the IMPLEMENTATION owns menu display (on Windows the
//     menu is shown by the backend right after fn — a modal TrackPopupMenu
//     on the tray thread; on Linux the DE shows the menu itself);
//   - the menu is append-only while running; handles stay valid;
//   - quit must be safe to call from any goroutine.
type backend interface {
    run(onReady func())
    quit()
    setTooltip(s string)
    setOnClick(fn func())
    setOnDClick(fn func())
    setOnRClick(fn func())
    addItem(title, tip string) menuHandle
    addCheckboxItem(title, tip string, checked bool) menuHandle
    addSeparator()
    addSubmenuItem(title, tip string) menuHandle
}

// menuHandle is a handle to one menu item created through backend.
type menuHandle interface {
    // addSubItem appends an entry to this item's submenu (valid on submenu
    // roots created via addSubmenuItem; on leaves it returns a no-op).
    addSubItem(title, tip string) menuHandle
    setTitle(s string)
    setTooltip(s string)
    enable()
    disable()
    setChecked(b bool)
    disabled() bool
    onClick(fn func())
}