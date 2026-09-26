package app

import "github.com/voxnord/cwidget/config"

// Private config-access helpers. All mutations of a.cfg that must survive
// a restart go through saveConfig — one place that owns cfgMu and the
// atomic write. Reading helpers (stayOpen, rememberPos, rememberedPos)
// keep window.go and the settings API from reaching into the config
// struct directly, so the schema can evolve without touching callers.

// saveConfig applies fn to the config under the lock and persists it.
// Returns the persist error, if any. fn runs with cfgMu held: keep it
// short and pure (no I/O besides the save itself, no callbacks).
func (a *App) saveConfig(fn func(c *config.Config)) error {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    fn(a.cfg)
    return config.Save(a.cfg)
}

// readConfig runs fn on a snapshot of the config under the lock.
func (a *App) readConfig(fn func(c *config.Config)) {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    fn(a.cfg)
}

func (a *App) stayOpen() bool {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    return a.cfg.StayOpen
}

func (a *App) rememberPos() bool {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    return a.cfg.RememberPos
}

// rememberedPos returns the persisted widget position and whether one exists.
func (a *App) rememberedPos() (has bool, x, y int) {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    return a.cfg.HasPos, a.cfg.PosX, a.cfg.PosY
}