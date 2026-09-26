//go:build windows

package app

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

// runSelfUninstall schedules the removal of the data folder and the
// executable AFTER this process exits (a running process cannot delete its
// own exe on Windows — the file is locked, and the webview child processes
// may keep data-dir files busy for a couple of seconds after our exit).
//
// Root cause of the previous failure: the script was spawned with
// DETACHED_PROCESS — a process with NO console at all. On Win10, cmd.exe
// executing a batch without a console may abort before running anything,
// and the console-based delays (timeout.exe) fail unconditionally. The
// manual run from Explorer worked because Explorer's cmd got a real
// console. The fix reproduces that known-good environment:
//   - CREATE_NO_WINDOW: the child gets a real (but hidden) console —
//     byte-for-byte the environment of the successful manual run;
//   - delays use ping -n (works console-less too), not timeout.exe;
//   - every step appends a marker to a log file, and the parent WAITS for
//     the "started" marker before exiting — a silently dead spawn is now
//     visible in the app log;
//   - a RunOnce registry entry remains as the last-resort finisher: if the
//     script is killed by something external, Windows completes the cleanup
//     at the next logon and removes the entry itself.
func runSelfUninstall(dataDir, exePath string) error {
    script := filepath.Join(os.TempDir(), "cwidget-uninstall.bat")
    logPath := filepath.Join(os.TempDir(), "cwidget-uninstall.log")
    runOnceKey := `Software\Microsoft\Windows\CurrentVersion\RunOnce`
    runOnceName := "CWidgetCleanup"

    content := strings.Join([]string{
        "@echo off",
        "echo [%date% %time%] cleanup started >> \"" + logPath + "\"",
        "ping -n 3 127.0.0.1 > nul",
        "rd /s /q \"" + dataDir + "\" >> \"" + logPath + "\" 2>&1",
        "ping -n 2 127.0.0.1 > nul",
        "rd /s /q \"" + dataDir + "\" >> \"" + logPath + "\" 2>&1",
        "ping -n 2 127.0.0.1 > nul",
        "rd /s /q \"" + dataDir + "\" >> \"" + logPath + "\" 2>&1",
        "del /f /q \"" + exePath + "\" >> \"" + logPath + "\" 2>&1",
        "reg delete HKCU\\" + runOnceKey + " /v " + runOnceName + " /f > nul 2>&1",
        "echo [%date% %time%] cleanup finished, errorlevel=%errorlevel% >> \"" + logPath + "\"",
        "del /f /q \"" + script + "\"",
        "",
    }, "\r\n")

    if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
        return err
    }

    // RunOnce backup: "%COMSPEC%" guarantees cmd is found at logon
    // regardless of PATH.
    if k, err := registry.OpenKey(registry.CURRENT_USER, runOnceKey, registry.SET_VALUE); err == nil {
        _ = k.SetStringValue(runOnceName, `"%COMSPEC%" /c "`+script+`"`)
        k.Close()
    } else {
        log.Printf("[app] RunOnce backup not set: %v", err)
    }

    // CREATE_NO_WINDOW (0x08000000): a hidden but REAL console for the child
    // — the exact environment of the confirmed-working manual run. Do NOT
    // use DETACHED_PROCESS here: a console-less cmd aborts batch scripts on
    // Win10 (see the comment above).
    cmd := exec.Command("cmd", "/c", script)
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
    if err := cmd.Start(); err != nil {
        log.Printf("[app] cleanup script spawn failed: %v", err)
        return err
    }

    // The first thing the script does is write the log marker. Wait for it:
    // our process is about to exit, and a spawn that died instantly must be
    // visible in the app log rather than silent.
    started := false
    for i := 0; i < 30; i++ { // up to 3s
        if _, err := os.Stat(logPath); err == nil {
            started = true
            break
        }
        time.Sleep(100 * time.Millisecond)
    }
    if started {
        log.Printf("[app] cleanup script started (script log: %s)", logPath)
    } else {
        log.Printf("[app] WARNING: cleanup script did not report start within 3s — the RunOnce entry remains as backup")
    }
    return nil
}