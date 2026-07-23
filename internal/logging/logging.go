// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package logging owns pawbar's process-wide zerolog logger. The bar
// process tees human-readable lines to the kitty panel pipe and to a
// persistent file under the user's state directory, so diagnostics
// survive a crash. Helper processes (menu overlays) log to the file
// only.
package logging

import (
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog"
)

// maxLogSize is the size past which the previous log is rotated aside
// on startup.
const maxLogSize = 1 << 20

// Log is the process-wide logger. It discards everything until Setup or
// SetupFileOnly replaces it, so libraries may log unconditionally.
var Log = zerolog.New(io.Discard)

// Setup wires the bar's logger: the panel pipe plus the state file.
// The level comes from PAWBAR_LOG_LEVEL (default info).
func Setup(console io.Writer) zerolog.Logger {
	writers := []io.Writer{zerolog.ConsoleWriter{Out: console, TimeFormat: time.TimeOnly}}
	if f := openLogFile(true); f != nil {
		writers = append(writers, zerolog.ConsoleWriter{Out: f, TimeFormat: time.DateTime, NoColor: true})
	}
	Log = newLogger(zerolog.MultiLevelWriter(writers...))
	return Log
}

// SetupFileOnly wires the logger for helper processes that have no
// panel pipe; proc tags every line so overlay logs are identifiable in
// the shared file.
func SetupFileOnly(proc string) zerolog.Logger {
	if f := openLogFile(false); f != nil {
		Log = newLogger(zerolog.ConsoleWriter{Out: f, TimeFormat: time.DateTime, NoColor: true}).
			With().Str("proc", proc).Logger()
	}
	return Log
}

func newLogger(w io.Writer) zerolog.Logger {
	level := zerolog.InfoLevel
	if s := os.Getenv("PAWBAR_LOG_LEVEL"); s != "" {
		if l, err := zerolog.ParseLevel(s); err == nil {
			level = l
		}
	}
	return zerolog.New(w).Level(level).With().Timestamp().Logger()
}

// FilePath returns the log file location,
// $XDG_STATE_HOME/pawbar/pawbar.log (or the ~/.local/state fallback).
func FilePath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "pawbar", "pawbar.log")
}

// openLogFile opens the log file for appending. Only the bar process
// rotates (helper processes must not race it aside). Any failure means
// file logging is skipped, never a startup failure.
func openLogFile(rotate bool) *os.File {
	path := FilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	if rotate {
		if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
			os.Rename(path, path+".old")
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// Go runs fn on a new goroutine, logging any panic with its stack
// instead of killing the process.
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)
		fn()
	}()
}

// Recover logs the in-flight panic, if any. Use as
// `defer logging.Recover("name")` at the top of long-lived goroutines.
func Recover(name string) {
	if p := recover(); p != nil {
		Log.Error().Str("goroutine", name).Str("stack", string(debug.Stack())).Msgf("panic: %v", p)
	}
}
