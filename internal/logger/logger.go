package logger

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Level represents log severity.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

var levelOrder = map[Level]int{
	LevelDebug: 0,
	LevelInfo:  1,
	LevelWarn:  2,
	LevelError: 3,
}

// Logger writes structured JSON logs.
type Logger struct {
	w     io.Writer
	mu    sync.Mutex
	level Level
}

// New creates a Logger that writes JSON to w.
func New(w io.Writer, level Level) *Logger {
	if w == nil {
		w = os.Stdout
	}
	return &Logger{w: w, level: level}
}

// Default returns a Logger writing to stdout at info level.
func Default() *Logger {
	return New(os.Stdout, LevelInfo)
}

func (l *Logger) enabled(lvl Level) bool {
	return levelOrder[lvl] >= levelOrder[l.level]
}

func (l *Logger) write(lvl Level, msg string, fields map[string]any) {
	if !l.enabled(lvl) {
		return
	}
	m := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     string(lvl),
		"message":   msg,
	}
	for k, v := range fields {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		log.Printf("logger marshal error: %v", err)
		return
	}
	l.mu.Lock()
	_, _ = l.w.Write(append(b, '\n'))
	l.mu.Unlock()
}

func (l *Logger) Debug(msg string, fields map[string]any) { l.write(LevelDebug, msg, fields) }
func (l *Logger) Info(msg string, fields map[string]any)  { l.write(LevelInfo, msg, fields) }
func (l *Logger) Warn(msg string, fields map[string]any)  { l.write(LevelWarn, msg, fields) }
func (l *Logger) Error(msg string, fields map[string]any) { l.write(LevelError, msg, fields) }
