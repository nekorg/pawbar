package powerprofiles

import (
	"github.com/nekorg/pawbar/internal/config"
	"github.com/nekorg/pawbar/internal/modules"
)

func init() {
	config.RegisterModule("powerprofiles", defaultOptions, func(o Options) (modules.Module, error) { return &powerProfileModule{opts: o}, nil })
}

type Options struct {
	Fg      config.Color                      `yaml:"fg"`
	Bg      config.Color                      `yaml:"bg"`
	Cursor  config.Cursor                     `yaml:"cursor"`
	Format  config.Format                     `yaml:"format"`
	OnClick config.MouseActions[MouseOptions] `yaml:"onmouse"`
}

type MouseOptions struct {
	Fg     *config.Color  `yaml:"fg"`
	Bg     *config.Color  `yaml:"bg"`
	Cursor *config.Cursor `yaml:"cursor"`
	Format *config.Format `yaml:"format"`
}

func defaultOptions() Options {
	pps, _ := config.NewTemplate("{{.Icon}}")
	return Options{
		Format:  config.Format{Template: pps},
		OnClick: config.MouseActions[MouseOptions]{},
	}
}
