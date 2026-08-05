# What is `pawbar`?

`pawbar` is a customizable status bar for GNU/Linux systems. Currently it supports wayland compositors but compatibility may be added for X.org and macOS systems.

# Installation

Currently, you have to manually compile and install `pawbar`.

## Manual
The following dependencies are required at compile time:
### Dependencies
 - `go`
 - `udev`
 - `librsvg`

At runtime, external-monitor brightness additionally needs the `i2c-dev`
kernel module and access to `/dev/i2c-*`; see the
[`backlight` module](/docs/modules#permissions) for the setup.


Clone and compile `pawbar`
```sh
git clone --recurse-submodules https://github.com/nekorg/pawbar
cd pawbar
go build .
```

Install using the installation script:
```sh
./install.sh
```

# Configuration

`pawbar` is configured using a configuration file.


Configuration file is placed at `$HOME/.config/pawbar/pawbar.yaml`

Simplest configuration file can be:
```yaml
right:
  - clock
```

It sets a right anchored `clock` module with default configuration

A useful default configuration can be:
```yaml
bar:
  gap: " "
  truncate_priority:
    - middle
    - right
    - left
left:
  - ws
  - title
middle:
  - clock:
      format: "{time:%a %H:%M}"
      states:
        hover: { format: "{time:%a %H:%M:%S}", fg: indianred }
right:
  - volume:
      on:
        right: { run: "pavucontrol" }
  - gap: " │ "
  - backlight
  - gap: " │ "
  - battery
```

The config is hot-reloaded on save, and `pawbar --check` validates it
without starting the bar. See [Configuration](/docs/configuration) for
the full schema.
