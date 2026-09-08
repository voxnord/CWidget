package app

import "github.com/you/currency-widget/autostart"

// Тонкие обёртки: app.go не знает о платформенных реализациях,
// а build tags сами подставляют нужный файл при сборке под ОС.

func autostartEnabled() bool  { return autostart.Enabled() }
func autostartEnable() error  { return autostart.Enable() }
func autostartDisable() error { return autostart.Disable() }