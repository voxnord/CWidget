//go:build linux

package tray

import "github.com/energye/systray"

// systrayBackend: Linux implementation of the backend contract over
// github.com/energye/systray (D-Bus / AppIndicator). Per-OS quirks:
//   - the icon format is .png (the ICO argument is ignored);
//   - the AppIndicator protocol does not report the click type to the app
//     in most DEs: the menu opens on ANY click by the DE's own will, and
//     the left/double-click callbacks fire where the DE supports
//     activation. The "Open" menu item keeps the functionality.

type systrayBackend struct {
    iconPNG []byte
}

func newBackend(iconICO, iconPNG []byte) backend {
    return &systrayBackend{iconPNG: iconPNG} // Linux: .png
}

func (b *systrayBackend) run(onReady func()) {
    systray.Run(func() {
        systray.SetIcon(b.iconPNG)
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
        menu.ShowMenu() // energye requires the caller to show it
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