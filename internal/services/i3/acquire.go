// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package i3

import "github.com/nekorg/pawbar/internal/services"

// Acquire returns the shared refcounted i3/sway service, starting it on
// first use. Call release when done (module Stop hook).
func Acquire() (*Service, func(), error) {
	return services.Acquire("i3", func() (*Service, error) {
		s := &Service{}
		if err := s.Start(); err != nil {
			return nil, err
		}
		return s, nil
	})
}
