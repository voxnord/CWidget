//go:build linux

package autostart

import (
	"os"
	"path/filepath"
	"strings"
)

const desktopName = "cwidget.desktop"

func desktopPath() (string, error) {
    cfg, err := os.UserConfigDir() // $XDG_CONFIG_HOME or ~/.config
    if err != nil {
        return "", err
    }
    return filepath.Join(cfg, "autostart", desktopName), nil
}

// escapeExec quotes the path for the Exec= line of a .desktop file.
func escapeExec(p string) string {
    p = strings.ReplaceAll(p, `\`, `\\`)
    p = strings.ReplaceAll(p, `"`, `\"`)
    return `"` + p + `"`
}

// Enable creates the XDG autostart .desktop entry.
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
        "Comment=Currency converter tray widget\n" +
        "Exec=" + escapeExec(exe) + "\n" +
        "Terminal=false\n" +
        "NoDisplay=false\n" +
        "X-GNOME-Autostart-enabled=true\n"
    return os.WriteFile(p, []byte(content), 0o644)
}

// Disable removes the .desktop entry. A missing file counts as success.
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

// Enabled reports whether the .desktop entry exists.
func Enabled() bool {
    p, err := desktopPath()
    if err != nil {
        return false
    }
    _, err = os.Stat(p)
    return err == nil
}