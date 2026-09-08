//go:build linux

package popup

import (
	"os"
	"os/exec"
	"time"
)

// RemoveFromTaskbar на X11 выставляет EWMH-состояние skip_taskbar через wmctrl (best-effort).
// На Wayland портативного способа нет — функция просто завершается без действий.
func RemoveFromTaskbar(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, err := exec.LookPath("wmctrl")
    if err != nil {
        return
    }
    go func() {
        for i := 0; i < 30; i++ {
            cmd := exec.Command(wmctrl, "-r", title, "-b", "add,skip_taskbar")
            if cmd.Run() == nil {
                return
            }
            time.Sleep(200 * time.Millisecond) // окно могло ещё не появиться
        }
    }()
}

// MoveNearTray — заглушка для Linux (позиционирование у трея зависит от DE).
func MoveNearTray(title string) {}

// FocusWindow активирует окно по заголовку (X11, best-effort через wmctrl).
func FocusWindow(title string) {
    if t := os.Getenv("XDG_SESSION_TYPE"); t == "wayland" || t == "" {
        return
    }
    wmctrl, err := exec.LookPath("wmctrl")
    if err != nil {
        return
    }
    _ = exec.Command(wmctrl, "-a", title).Run()
}