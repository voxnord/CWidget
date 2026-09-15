//go:build windows

// Package autostart manages "run at system startup" per platform:
// Windows — the HKCU\...\Run registry value; Linux — an XDG autostart
// .desktop file.
package autostart

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
    runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
    valueName  = "CWidget"
)

// Enable turns autostart on.
func Enable() error {
    exe, err := os.Executable()
    if err != nil {
        return err
    }
    k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
    if err != nil {
        return err
    }
    defer k.Close()
    return k.SetStringValue(valueName, `"`+exe+`"`)
}

// Disable turns autostart off. A missing value counts as success.
func Disable() error {
    k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
    if err != nil {
        return nil // no key — no autostart anyway
    }
    defer k.Close()
    if err := k.DeleteValue(valueName); err != nil {
        return nil
    }
    return nil
}

// Enabled reports the actual OS state (the source of truth is the registry,
// not config.json).
func Enabled() bool {
    k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
    if err != nil {
        return false
    }
    defer k.Close()
    v, _, err := k.GetStringValue(valueName)
    return err == nil && v != ""
}