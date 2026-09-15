package app

import "github.com/voxnord/cwidget/autostart"

// Thin wrappers: app.go does not need to know about platform-specific
// implementations — build tags pick the right file at compile time.

func autostartEnabled() bool  { return autostart.Enabled() }
func autostartEnable() error  { return autostart.Enable() }
func autostartDisable() error { return autostart.Disable() }