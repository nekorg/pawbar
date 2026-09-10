// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

// Package session locates this compositor session's runtime files. One
// pawbar owns one display, so everything keyed to a running pawbar — its
// lock, its panel-broker socket — is named the same way, and parallel
// compositor sessions stay independent.
package session

import (
	"os"
	"path/filepath"
	"strings"
)

// RuntimePath returns the session's runtime file with the given extension,
// e.g. RuntimePath("lock") -> $XDG_RUNTIME_DIR/pawbar-wayland-1.lock.
func RuntimePath(ext string) string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	name := "pawbar"
	if k := Key(); k != "" {
		name += "-" + k
	}
	return filepath.Join(dir, name+"."+ext)
}

// Key names this compositor session, empty when there is nothing to name it
// by. Anything else keyed to a running pawbar is built from it.
func Key() string {
	if d := os.Getenv("WAYLAND_DISPLAY"); d != "" {
		return filepath.Base(d)
	}
	if d := os.Getenv("DISPLAY"); d != "" {
		return strings.TrimPrefix(d, ":")
	}
	return ""
}
