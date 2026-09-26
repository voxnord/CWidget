//go:build windows

package tray

import "github.com/energye/systray"

// systrayBackend: Windows implementation of the backend contract over
// github.com/energye/systray. Per-OS quirks encapsulated HERE and nowhere
// else:
//   - the icon format is .ico (the PNG argument is ignored);
//   - left/double-click callbacks open the window and MUST NOT show the
//     menu themselves;
//   - the right-click handler owns the menu display: ShowMenu runs on the
//     tray thread (the menu is a modal TrackPopupMenu there).
//
// History note: this library's Windows layer has a known failure mode for
// us (the icon stops delivering clicks; no re-add after an explorer.exe
// restart — see the project diagnostics history). Thanks to the contract
// in backend.go, replacing this file with a self-contained Win32
// implementation is a single-file change.

type systrayBackend struct {
    iconICO []byte
}

func newBackend(iconICO, iconPNG []byte) backend {
    return &systrayBackend{iconICO: iconICO} // Windows: .ico
}

func (b *systrayBackend) run(onReady func()) {
    systray.Run(func() {
        systray.SetIcon(b.iconICO)
        onReady()
    }, nil)
}

func (b *systrayBackend) quit() { systray.Quit() }

func (b *systrayBackend) setTooltip(s string) { systray.SetTooltip(s) }

func (b *systrayBackend) setOnClick(fn func()) {
    systray.SetOnClick(func(_ systray.IMenu) { fn() })
}

func (b *systrayBackend) setOnDClick(fn func()) {
    systray.SetOnDClick(func(_ systray.IMenu) { fn() })
}

func (b *systrayBackend) setOnRClick(fn func()) {
    systray.SetOnRClick(func(menu systray.IMenu) {
        if fn != nil {
            fn()
        }
        menu.ShowMenu() // on the tray thread, before the loop resumes
    })
}

func (b *systrayBackend) addItem(title, tip string) menuHandle {
    return &systrayItem{m: systray.AddMenuItem(title, tip)}
}

func (b *systrayBackend) addCheckboxItem(title, tip string, checked bool) menuHandle {
    return &systrayItem{m: systray.AddMenuItemCheckbox(title, tip, checked)}
}

func (b *systrayBackend) addSeparator() { systray.AddSeparator() }

func (b *systrayBackend) addSubmenuItem(title, tip string) menuHandle {
    return &systrayItem{m: systray.AddMenuItem(title, tip)}
}

type systrayItem struct{ m *systray.MenuItem }

func (i *systrayItem) addSubItem(title, tip string) menuHandle {
    if i == nil || i.m == nil {
        return &systrayItem{}
    }
    return &systrayItem{m: i.m.AddSubMenuItem(title, tip)}
}

func (i *systrayItem) setTitle(s string) {
    if i != nil && i.m != nil {
        i.m.SetTitle(s)
    }
}

func (i *systrayItem) setTooltip(s string) {
    if i != nil && i.m != nil {
        i.m.SetTooltip(s)
    }
}

func (i *systrayItem) enable() {
    if i != nil && i.m != nil {
        i.m.Enable()
    }
}

func (i *systrayItem) disable() {
    if i != nil && i.m != nil {
        i.m.Disable()
    }
}

func (i *systrayItem) setChecked(b bool) {
    if i != nil && i.m != nil {
        if b {
            i.m.Check()
        } else {
            i.m.Uncheck()
        }
    }
}

func (i *systrayItem) disabled() bool {
    if i == nil || i.m == nil {
        return false
    }
    return i.m.Disabled()
}

func (i *systrayItem) onClick(fn func()) {
    if i != nil && i.m != nil {
        i.m.Click(fn)
    }
}