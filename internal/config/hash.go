// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package config

import (
	"crypto/sha256"
	"encoding/hex"

	"gopkg.in/yaml.v3"
)

// entryHash fingerprints a module entry (name + canonical remarshal of its
// options subtree). Hot reload compares hashes to decide whether an
// instance's configuration changed.
func entryHash(e ModuleEntry) string {
	h := sha256.New()
	h.Write([]byte(e.Name))
	h.Write([]byte{0})
	if e.Node != nil {
		if b, err := yaml.Marshal(e.Node); err == nil {
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
