// Package logfile is a log file that cannot grow without bound: the
// background watch runs for weeks, and nobody is there to trim its log.
package logfile

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

type file struct {
	mu    sync.Mutex
	path  string
	limit int64
	f     *os.File
	size  int64
}

// Open appends to path. When a write would push the file past limit bytes, the
// file becomes path+".1", replacing any earlier one, and a fresh file starts,
// so at most about twice limit is kept on disk. It creates the directory if
// needed.
func Open(path string, limit int64) (io.WriteCloser, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &file{path: path, limit: limit}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *file) open() error {
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

func (l *file) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// An empty file is never rotated, so one write larger than limit does not
	// rotate again and again.
	if l.size > 0 && l.size+int64(len(p)) > l.limit {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate closes before renaming because Windows will not rename an open file.
// A rename that fails (on Windows, a tail holding the file open) is not an
// error for the caller: the log keeps growing past the limit rather than
// dropping lines, and the next write tries again.
func (l *file) rotate() error {
	if err := l.f.Close(); err != nil {
		return err
	}
	os.Rename(l.path, l.path+".1")
	return l.open()
}

func (l *file) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
