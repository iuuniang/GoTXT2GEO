/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package logger

import (
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/lmittmann/tint"
	"golang.org/x/term"
)

var (
	log  *slog.Logger
	once sync.Once
)

const DateTimeMilli = "2006-01-02 15:04:05.000"

// Init 根据级别初始化全局日志。
// level: debug|info|warn|error
func Init(level string) {
	lvl := slog.LevelInfo
	logLevelStr := strings.ToLower(strings.TrimSpace(level))

	switch logLevelStr {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}

	handler := tint.NewHandler(os.Stdout, &tint.Options{
		AddSource:  logLevelStr == "debug",
		Level:      lvl,
		NoColor:    !isTerminalColorSupported(),
		TimeFormat: DateTimeMilli,
		// ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		// 	if a.Key == slog.LevelKey && len(groups) == 0 {
		// 		level, ok := a.Value.Any().(slog.Level)
		// 		if ok && level == slog.LevelDebug {
		// 			return tint.Attr(13, slog.String(a.Key, "DBG"))
		// 		}
		// 	}
		// 	return a
		// },
	})

	log = slog.New(handler)
}

// isTerminalColorSupported checks if terminal supports color output
func isTerminalColorSupported() bool {
	fd := os.Stdout.Fd()
	// Ensure the file descriptor is within the valid int range
	if fd > uintptr(^uint(0)>>1) {
		return false // File descriptor too large, assume not a terminal
	}
	return term.IsTerminal(int(fd))
}

// ensure 初始化默认 logger（仅在第一次访问且未手动 Init 时）。
func ensure() {
	once.Do(func() {
		if log == nil {
			Init("info") // 默认级别
		}
	})
}

// L 返回全局 logger。
func Log() *slog.Logger {
	ensure()
	return log
}

// Helper wrappers
func Debug(msg string, args ...any) { Log().Debug(msg, args...) }
func Info(msg string, args ...any)  { Log().Info(msg, args...) }
func Warn(msg string, args ...any)  { Log().Warn(msg, args...) }
func Error(msg string, args ...any) { Log().Error(msg, args...) }
