package app

import (
	"log"
	"os"
	"runtime/debug"
	"sync"
)

// LogFileCap is the size limit of the log file. The app writes only a few
// lines per hour, so reaching the cap takes months of uptime; when it
// happens the file is truncated and marked. Full rotation machinery is not
// worth the code at this volume.
const LogFileCap = 512 << 10 // 512 KB

// CapWriter wraps a log file and truncates it when the cap is exceeded.
type CapWriter struct {
    mu   sync.Mutex
    f    *os.File
    path string
    n    int64
}

// NewCapWriter opens (or creates) the log file in APPEND mode — the log of
// the previous session is preserved, which is exactly what a post-mortem of
// "it broke while idle in autostart" needs.
func NewCapWriter(path string) (*CapWriter, error) {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
    if err != nil {
        return nil, err
    }
    st, err := f.Stat()
    if err != nil {
        f.Close()
        return nil, err
    }
    return &CapWriter{f: f, path: path, n: st.Size()}, nil
}

func (w *CapWriter) Write(p []byte) (int, error) {
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.n+int64(len(p)) > LogFileCap {
        w.f.Close()
        f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
        if err != nil {
            return 0, err
        }
        w.f = f
        w.n = 0
        marker := []byte("--- log truncated (size cap reached) ---\n")
        _, _ = f.Write(marker)
        w.n = int64(len(marker))
    }
    n, err := w.f.Write(p)
    w.n += int64(n)
    return n, err
}

// Close flushes and closes the underlying file.
func (w *CapWriter) Close() error {
    w.mu.Lock()
    defer w.mu.Unlock()
    return w.f.Close()
}

// Safe runs fn and converts a panic into a log record with the stack.
// An uncaught panic in ANY goroutine terminates the whole process — for a
// tray widget that looks like "nothing responds, nothing visible" and leaves
// a zombie icon behind. Wrap every goroutine whose silent death would go
// unnoticed: tray handlers, the ticker loop, retry timers.
func Safe(what string, fn func()) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("[panic] %s: %v\n%s", what, r, debug.Stack())
        }
    }()
    fn()
}