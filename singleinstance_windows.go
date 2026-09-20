//go:build windows

package main

import (
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mutexName — kernel mutex guarding single-instance behavior. "Local\"
// scopes it to the current session.
const mutexName = `Local\CWidget-single-instance`

var (
    kernel32         = windows.NewLazySystemDLL("kernel32.dll")
    procCreateMutexW = kernel32.NewProc("CreateMutexW")
)

// acquireSingleInstance returns true when this process is the owner (the
// first instance). A second launch — manual while the autostart copy is
// alive — exits immediately instead of running a second tray icon and a
// second window with the SAME title (window-by-title lookups would then hit
// the wrong process).
//
// The RAW CreateMutexW is used deliberately: the x/sys/windows wrapper
// swallows GetLastError when a valid handle is returned, and
// ERROR_ALREADY_EXISTS is exactly the signal we need (it arrives WITH a
// valid handle — that is the Win32 convention). The nilness analyzer
// flagged the previous err==ERROR_ALREADY_EXISTS check as dead code, which
// meant the guard never fired.
func acquireSingleInstance() bool {
    name, err := windows.UTF16PtrFromString(mutexName)
    if err != nil {
        return true // fail open: better two instances than none
    }
    h, _, errno := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(name))) // 1 = initial owner
    if h == 0 {
        log.Printf("[start] failed to create the instance mutex: %v", errno)
        return true // fail open
    }
    if errno == windows.ERROR_ALREADY_EXISTS {
        _ = windows.CloseHandle(windows.Handle(h))
        log.Printf("[start] another instance is already running — exiting")
        return false
    }
    // Keep the handle unclosed for the process lifetime: it holds the lock.
    return true
}