package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestLoggerJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, LevelInfo)
	log.Info("test message", map[string]any{"key": "value"})

	line := strings.TrimSpace(buf.String())
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("expected valid JSON, got %s: %v", line, err)
	}
	if m["level"] != "info" {
		t.Errorf("expected level info, got %v", m["level"])
	}
	if m["message"] != "test message" {
		t.Errorf("expected message 'test message', got %v", m["message"])
	}
	if m["key"] != "value" {
		t.Errorf("expected key value, got %v", m["key"])
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, LevelWarn)
	log.Info("should not appear", nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for info when level is warn")
	}
	log.Warn("should appear", nil)
	if !strings.Contains(buf.String(), "should appear") {
		t.Error("expected warn message to appear")
	}
}

func TestDefaultLogger(t *testing.T) {
	log := Default()
	if log == nil {
		t.Error("expected non-nil default logger")
	}
}

func TestLoggerConcurrency(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, LevelInfo)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			log.Info("concurrent", map[string]any{"n": n})
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 100 {
		t.Fatalf("expected 100 log lines, got %d", len(lines))
	}
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("expected valid JSON line: %s: %v", line, err)
		}
	}
}

func TestLoggerNilFields(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, LevelInfo)
	log.Info("nil fields", nil)
	if !strings.Contains(buf.String(), "nil fields") {
		t.Error("expected message with nil fields to log")
	}
}
