//go:build linux

package autostart

import (
	"os"
	"path/filepath"
	"strings"
)

const desktopName = "cwidget.desktop"

func desktopPath() (string, error) {
    cfg, err := os.UserConfigDir() // $XDG_CONFIG_HOME либо ~/.config
    if err != nil {
        return "", err
    }
    return filepath.Join(cfg, "autostart", desktopName), nil
}

// escapeExec квотирует путь для строки Exec= файла .desktop.
func escapeExec(p string) string {
    p = strings.ReplaceAll(p, `\`, `\\`)
    p = strings.ReplaceAll(p, `"`, `\"`)
    return `"` + p + `"`
}

func Enable() error {
    p, err := desktopPath()
    if err != nil {
        return err
    }
    if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
        return err
    }
    exe, err := os.Executable()
    if err != nil {
        return err
    }
    content := "[Desktop Entry]\n" +
        "Type=Application\n" +
        "Version=1.0\n" +
        "Name=CWidget\n" +
        "Comment=Tray currency converter\n" +
        "Exec=" + escapeExec(exe) + "\n" +
        "Terminal=false\n" +
        "NoDisplay=false\n" +
        "X-GNOME-Autostart-enabled=true\n"
    return os.WriteFile(p, []byte(content), 0o644)
}

func Disable() error {
    p, err := desktopPath()
    if err != nil {
        return err
    }
    if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
        return err
    }
    return nil
}

func Enabled() bool {
    p, err := desktopPath()
    if err != nil {
        return false
    }
    _, err = os.Stat(p)
    return err == nil
}