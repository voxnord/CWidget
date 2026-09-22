//go:build windows

package app

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// runSelfUninstall schedules the removal of the data folder and the
// executable AFTER this process exits (a running process cannot delete its
// own exe on Windows — the file is locked, and the webview child processes
// may keep data-dir files busy for a couple of seconds after our exit).
//
// Two independent mechanisms (belt and suspenders):
//  1. a detached cmd script with THREE deletion passes and pauses between
//     them (enough for the webview children to release the files), tracing
//     its progress into its own log for post-mortem;
//  2. a RunOnce registry entry: if the script could not finish (a stubborn
//     file handle, an antivirus hold), Windows itself finishes the cleanup
//     at the next logon; the script deletes the entry when it succeeds.
//
// Win32 subtleties that bit us before (all fixed here):
//   - HideWindow:true (CREATE_NO_WINDOW) and CreationFlags:DETACHED_PROCESS
//     are MUTUALLY EXCLUSIVE — combined, CreateProcess fails with
//     ERROR_INVALID_PARAMETER and nothing is spawned at all. Use
//     CREATE_NO_WINDOW only: cmd gets a hidden console and runs normally.
//   - `timeout /t N` requires console input and aborts in console-less
//     contexts; `ping -n N 127.0.0.1` is the classic console-independent
//     delay.
func runSelfUninstall(dataDir, exePath string) error {
    script := filepath.Join(os.TempDir(), "cwidget-uninstall.bat")
    logPath := filepath.Join(os.TempDir(), "cwidget-uninstall.log")
    runOnceKey := `Software\Microsoft\Windows\CurrentVersion\RunOnce`
    runOnceName := "CWidgetCleanup"

    content := strings.Join([]string{
        "@echo off",
        "echo [%date% %time%] cleanup started >> \"" + logPath + "\"",
        "ping -n 3 127.0.0.1 > nul", // ~2s: let our process and the webview children exit
        "rd /s /q \"" + dataDir + "\"",
        "ping -n 2 127.0.0.1 > nul",
        "rd /s /q \"" + dataDir + "\"",
        "ping -n 2 127.0.0.1 > nul",
        "rd /s /q \"" + dataDir + "\"",
        "del /f /q \"" + exePath + "\"",
        "reg delete HKCU\\" + runOnceKey + " /v " + runOnceName + " /f > nul 2>&1",
        "echo [%date% %time%] cleanup finished, errorlevel=%errorlevel% >> \"" + logPath + "\"",
        "del /f /q \"" + script + "\"",
        "", // trailing CRLF for the last line
    }, "\r\n")

    if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
        return err
    }

    // RunOnce backup: "%COMSPEC% /c ..." so cmd itself is guaranteed to be
    // found at logon regardless of PATH.
    if k, err := registry.OpenKey(registry.CURRENT_USER, runOnceKey, registry.SET_VALUE); err == nil {
        _ = k.SetStringValue(runOnceName, `"%COMSPEC%" /c "`+script+`"`)
        k.Close()
    } else {
        log.Printf("[app] RunOnce backup not set: %v", err)
    }

    // CREATE_NO_WINDOW only (via HideWindow) — see the comment above.
    cmd := exec.Command("cmd", "/c", script)
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
    if err := cmd.Start(); err != nil {
        // This error MUST be visible: log it here — the caller may already
        // be shutting the app down.
        log.Printf("[app] failed to spawn the cleanup script: %v", err)
        return err
    }
    log.Printf("[app] cleanup script spawned (pid=%d)", cmd.Process.Pid)
    return nil
}