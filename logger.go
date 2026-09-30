package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger 是一个极简的滚动文件日志器，同时输出到 stdout。
type Logger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
}

func NewLogger(path string, maxBytes int64) *Logger {
	return &Logger{path: path, maxBytes: maxBytes}
}

func (l *Logger) Logf(level, format string, args ...any) {
	line := fmt.Sprintf("%s [%s] %s\n",
		time.Now().Format("2006-01-02 15:04:05"), level, fmt.Sprintf(format, args...))

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.maxBytes > 0 {
		if fi, err := os.Stat(l.path); err == nil && fi.Size() > l.maxBytes {
			_ = os.Rename(l.path, l.path+".old")
		}
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprint(os.Stderr, line)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		fmt.Fprint(os.Stderr, line)
		return
	}
	fmt.Print(line)
}

func (l *Logger) Infof(f string, a ...any)  { l.Logf("INFO", f, a...) }
func (l *Logger) Warnf(f string, a ...any)  { l.Logf("WARN", f, a...) }
func (l *Logger) Errorf(f string, a ...any) { l.Logf("ERROR", f, a...) }
