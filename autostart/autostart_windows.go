//go:build windows

package autostart

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
    runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
    valueName  = "CurrencyWidget"
)

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

func Disable() error {
    k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
    if err != nil {
        return nil // ключа нет — автозапуска и так нет
    }
    defer k.Close()
    if err := k.DeleteValue(valueName); err != nil {
        return nil
    }
    return nil
}

func Enabled() bool {
    k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
    if err != nil {
        return false
    }
    defer k.Close()
    v, _, err := k.GetStringValue(valueName)
    return err == nil && v != ""
}