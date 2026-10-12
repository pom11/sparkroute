// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package sparkrun

import (
	"github.com/sparksq/sparkroute/internal/privatepath"
	"os"
	"path/filepath"
)

// privatepath.Check validates the owner and DACL on Windows.
func checkDaemonFileOwner(_ os.FileInfo) error { return nil }

func checkDaemonAncestors(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	return privatepath.Check(parent, info.Mode())
}
