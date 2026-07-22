// Copyright (c) 2025 Nekorg All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// SPDX-License-Identifier: bsd

package locale

import (
	"os"
	"strings"
	"time"

	"github.com/nekorg/pawbar/pkg/module"
)

type localeModule struct {
	opts   *Options
	ticker *module.Ticker
	locale string
}

func (m *localeModule) Init(ctx *module.Ctx) error {
	m.opts = ctx.Options().(*Options)
	m.ticker = module.NewTicker(m.opts.Tick.Go())
	m.locale = currentLocale()
	module.On(ctx, m.ticker.Source(), func(time.Time) { m.locale = currentLocale() })
	return nil
}

func (m *localeModule) OnState(ctx *module.Ctx) {
	m.opts = ctx.Options().(*Options)
	m.ticker.Set(m.opts.Tick.Go())
}

func (m *localeModule) Render(w *module.Writer) {
	w.Text(module.P{"locale": m.locale})
}

func currentLocale() string {
	locale := langFromEnv()
	if locale == "C" || locale == "POSIX" || locale == "" {
		return ""
	}
	first := strings.Split(locale, ":")[0]
	language, region := splitLocale(first)
	if region != "" {
		return language + "-" + region
	}
	return language
}

func langFromEnv() string {
	locale := ""
	for _, env := range [...]string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale = os.Getenv(env); locale != "" {
			break
		}
	}
	if locale == "C" || locale == "POSIX" {
		return locale
	}
	if languages := os.Getenv("LANGUAGE"); languages != "" {
		return languages
	}
	return locale
}

func splitLocale(locale string) (language, territory string) {
	formatted, _, _ := strings.Cut(locale, ".")
	formatted = strings.ReplaceAll(formatted, "-", "_")
	language, territory, _ = strings.Cut(formatted, "_")
	return language, territory
}
