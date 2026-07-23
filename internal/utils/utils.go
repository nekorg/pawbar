// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package utils

import (
	"cmp"
)

func Clamp[T cmp.Ordered](n, low, high T) T {
	if n < low {
		return low
	}

	if n > high {
		return high
	}

	return n
}
