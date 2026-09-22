//go:build linux

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// runSelfUninstall schedules the removal of the data folder and the binary
// after this process exits (on Linux deleting a running binary is legal,
// but a detached script keeps the flow identical on both platforms).
func runSelfUninstall(dataDir, exePath string) error {
    script := filepath.Join(os.TempDir(), "cwidget-uninstall.sh")
    content := "#!/bin/sh\n" +
        "sleep 2\n" +
        "rm -rf \"" + dataDir + "\"\n" +
        "rm -f \"" + exePath + "\"\n" +
        "rm -f \"" + script + "\"\n"
    if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
        return err
    }
    cmd := exec.Command("sh", script)
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
    return cmd.Start() // detached: survives our exit
}