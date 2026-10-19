// SPDX-License-Identifier: GPL-3.0-or-later

package ptnopserver

import (
	"log/slog"
	"sync"
	"time"
)

// decimateLogger is a [pipelineSLogger] optimized for measuring speeds.
//
// We include all info level events and drop debug events except for readDone
// and writeDone. We decimate readDone and writeDone to reduce the events volume
// but we always include events that contain an error.
//
// Construct using [newDecimateLogger].
type decimateLogger struct {
	mu sync.Mutex
	lr time.Time
	lw time.Time
	pl pipelineSLogger
}

// newDecimateLogger creates a new [*decimateLogger].
func newDecimateLogger(parent pipelineSLogger) *decimateLogger {
	return &decimateLogger{
		mu: sync.Mutex{},
		lr: time.Time{},
		lw: time.Time{},
		pl: parent,
	}
}

var _ pipelineSLogger = &decimateLogger{}

// Debug implements [pipelineSLogger].
func (s *decimateLogger) Debug(msg string, args ...any) {
	if s.shouldKeepEvent(msg, args...) {
		s.pl.Debug(msg, args...)
	}
}

// Info implements [pipelineSLogger].
func (s *decimateLogger) Info(msg string, args ...any) {
	s.pl.Info(msg, args...)
}

// Warn implements [pipelineSLogger].
func (s *decimateLogger) Warn(msg string, args ...any) {
	s.pl.Warn(msg, args...)
}

// Error implements [pipelineSLogger].
func (s *decimateLogger) Error(msg string, args ...any) {
	s.pl.Error(msg, args...)
}

func (s *decimateLogger) shouldKeepEvent(msg string, args ...any) bool {
	switch msg {
	case "readDone":
		return s.containsError(args...) || s.shouldKeep(&s.lr)
	case "writeDone":
		return s.containsError(args...) || s.shouldKeep(&s.lw)
	default:
		return false
	}
}

func (s *decimateLogger) containsError(args ...any) bool {
	for _, entry := range args {
		if attr, ok := entry.(slog.Attr); ok {
			if attr.Key == "errClass" && attr.Value.Kind() == slog.KindString {
				return attr.Value.String() != ""
			}
		}
	}
	return false
}

func (s *decimateLogger) shouldKeep(last *time.Time) bool {
	const minSamplingInterval = 250 * time.Millisecond
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(*last) < minSamplingInterval {
		return false
	}
	*last = time.Now()
	return true
}
