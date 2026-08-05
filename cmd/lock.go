// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// acquireLock takes the per-session pawbar lock. One pawbar owns every
// monitor, so a second one would stack a duplicate bar on each output
// instead of doing anything useful. The lock is keyed by wayland display
// so parallel compositor sessions stay independent, and it is released by
// the kernel when the process dies — no stale lock files to clean up.
//
// It returns the still-open lock file (which must stay open for the
// process's lifetime) or the pid holding the lock.
func acquireLock() (*os.File, int, error) {
	path := lockPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := readLockPid(f)
		f.Close()
		return nil, pid, fmt.Errorf("another pawbar holds %s", path)
	}
	if err := f.Truncate(0); err == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
	}
	return f, 0, nil
}

func lockPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	name := "pawbar.lock"
	if d := os.Getenv("WAYLAND_DISPLAY"); d != "" {
		name = "pawbar-" + filepath.Base(d) + ".lock"
	} else if d := os.Getenv("DISPLAY"); d != "" {
		name = "pawbar-" + strings.TrimPrefix(d, ":") + ".lock"
	}
	return filepath.Join(dir, name)
}

// readLockPid reads the pid the lock holder wrote, 0 when unreadable.
func readLockPid(f *os.File) int {
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 0)
	if n == 0 && err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}
