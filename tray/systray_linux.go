//go:build linux

package tray

import (
	"github.com/energye/systray"
)

// Linux backend: energye/systray over D-Bus (AppIndicator). The Windows
// shell-layer failures of this library do not apply here (different
// transport, different DE-side implementation), so it stays.

// sysMenu wraps an energye/systray menu item. No local state mirrors:
// checked/disabled live inside the library item, the methods delegate.
type sysMenu struct {
    m *systray.MenuItem
}

func sysRun(onReady func()) { systray.Run(onReady, nil) }
func sysQuit()              { systray.Quit() }
func sysSetIcon(b []byte)   { systray.SetIcon(b) }
func sysSetTooltip(s string) { systray.SetTooltip(s) }

func sysSetOnClick(fn func())  { systray.SetOnClick(func(_ systray.IMenu) { fn() }) }
func sysSetOnDClick(fn func()) { systray.SetOnDClick(func(_ systray.IMenu) { fn() }) }

// sysSetOnRClick: fn runs first, then the menu is shown (energye requires
// the caller to invoke ShowMenu itself).
func sysSetOnRClick(fn func()) {
    systray.SetOnRClick(func(menu systray.IMenu) {
        fn()
        menu.ShowMenu()
    })
}

func sysAddItem(title, tip string) *sysMenu {
    return &sysMenu{m: systray.AddMenuItem(title, tip)}
}

func sysAddCheckboxItem(title, tip string, checked bool) *sysMenu {
    return &sysMenu{m: systray.AddMenuItemCheckbox(title, tip, checked)}
}

func sysAddSeparator() *sysMenu {
    systray.AddSeparator()
    return &sysMenu{}
}

func sysAddSubmenuItem(title, tip string) *sysMenu {
    return &sysMenu{m: systray.AddMenuItem(title, tip)}
}

func (m *sysMenu) addSubItem(title, tip string) *sysMenu {
    if m == nil || m.m == nil {
        return &sysMenu{}
    }
    return &sysMenu{m: m.m.AddSubMenuItem(title, tip)}
}

func (m *sysMenu) setTitle(s string) {
    if m != nil && m.m != nil {
        m.m.SetTitle(s)
    }
}

func (m *sysMenu) setTooltip(s string) {
    if m != nil && m.m != nil {
        m.m.SetTooltip(s)
    }
}

func (m *sysMenu) enable() {
    if m != nil && m.m != nil {
        m.m.Enable()
    }
}

func (m *sysMenu) disable() {
    if m != nil && m.m != nil {
        m.m.Disable()
    }
}

func (m *sysMenu) setChecked(b bool) {
    if m != nil && m.m != nil {
        if b {
            m.m.Check()
        } else {
            m.m.Uncheck()
        }
    }
}

func (m *sysMenu) disabled() bool {
    if m != nil && m.m != nil {
        return m.m.Disabled()
    }
    return false
}

func (m *sysMenu) onClick(fn func()) {
    if m != nil && m.m != nil {
        m.m.Click(fn)
    }
}