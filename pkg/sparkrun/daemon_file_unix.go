// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package sparkrun

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func checkDaemonFileOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("daemon connection must belong to the current user")
	}
	return nil
}

// An owner-only leaf must not live in another user's replaceable directory.
// Root-owned standard symlinks (for example macOS /var) are resolved first;
// the resolved ancestors still need trusted ownership and write permissions.
func checkDaemonAncestors(path string) error {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	protected := false
	parents := []string{}
	for {
		parents = append(parents, parent)
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	for index := len(parents) - 1; index >= 0; index-- {
		info, err := os.Lstat(parents[index])
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return fmt.Errorf("unsafe daemon directory owner")
		}
		stickyRoot := stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0022 != 0 && !stickyRoot && !protected {
			return fmt.Errorf("unsafe writable daemon directory")
		}
		if stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0 {
			protected = true
		}
	}
	return nil
}
