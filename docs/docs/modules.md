---
prev:
  text: 'Configuration'
  link: '/docs/configuration'
next: false
---

# Module reference

Every module accepts the common [block keys](/docs/configuration#blocks-style--format)
(`fg`, `bg`, `format`, ...), a `states:` mapping and an `on:` mapping.
This page lists what each module adds on top: its options, placeholders,
condition states and verbs.

Each section starts with the module's **shipped defaults**: the yaml file
embedded in pawbar that forms the bottom layer of the cascade for that
module. It is real config in exactly the syntax you write; a bare module
name behaves exactly as if you pasted it. Print it any time with
`pawbar defaults <name>`, override any key in your entry, unbind a shipped
`on:` binding with `~`, or drop the whole layer with `defaults: false`
(see [configuration](/docs/configuration#shipped-defaults)).

## `backlight`

Screen brightness via sysfs and udev.

Shipped defaults:

```yaml
format: "{icon} {light}%"
icons: ["󰃞", "󰃟", "󰃝", "󰃠"]
on:
  scroll-up: { run: "brightnessctl set +5%" }
  scroll-down: { run: "brightnessctl set 5%-" }
```

| Option | Default | Description |
|---|---|---|
| `icons` | `["󰃞", "󰃟", "󰃝", "󰃠"]` | icon ramp, picked by brightness level |

| Placeholder | Description |
|---|---|
| `{icon}` | brightness level icon |
| `{light}` | brightness percentage |
| `{now}` | raw brightness value |
| `{max}` | raw maximum brightness |

## `battery`

Battery level via upower.

Shipped defaults:

```yaml
format: "{icon} {bat}%"
discharging_icons: ["󰂃", "󰁺", "󰁻", "󰁼", "󰁽", "󰁾", "󰁿", "󰂀", "󰂁", "󰂂", "󰁹"]
charging_icons: ["󰢟", "󰢜", "󰂆", "󰂇", "󰂈", "󰢝", "󰂉", "󰢞", "󰂊", "󰂋", "󰂅"]
charged_icon: "󱟢"
warn_at: 30
low_at: 15
states:
  warn: { fg: "@warning" }
  low: { fg: "@urgent" }
  hover: { format: "{hours} hrs {minutes} mins" }
```

| Option | Default | Description |
|---|---|---|
| `discharging_icons` | `["󰂃", "󰁺", ... "󰁹"]` | icon ramp while discharging |
| `charging_icons` | `["󰢟", "󰢜", ... "󰂅"]` | icon ramp while charging |
| `charged_icon` | `"󱟢"` | icon when fully charged |
| `warn_at` | `30` | percentage that turns on the `warn` state |
| `low_at` | `15` | percentage that turns on the `low` state |

| Placeholder | Description |
|---|---|
| `{icon}` | battery level icon |
| `{bat}` | battery percentage |
| `{hours}` | hours until full/empty |
| `{minutes}` | minutes until full/empty (0-59) |

| State | Shipped styling | When |
|---|---|---|
| `warn` | `fg: "@warning"` | at or below `warn_at` |
| `low` | `fg: "@urgent"` | at or below `low_at` |
| `charging` | none | plugged in and charging |
| `charged` | none | fully charged |
| `hover` | ETA format (see above) | pointer over the module |

## `bluetooth`

Adapter and device status via bluez.

Shipped defaults:

```yaml
format: "󰂱"
states:
  disconnected: { format: "" }
  off: { fg: darkgray, format: "󰂲" }
```

No options.

| Placeholder | Description |
|---|---|
| `{device}` | connected device name |

| State | Shipped styling | When |
|---|---|---|
| `disconnected` | empty format (hidden) | powered, nothing connected |
| `off` | `fg: darkgray`, format `󰂲` | adapter powered off |

Add a user state to show the device name on demand:

```yaml
- bluetooth:
    states:
      detail: { format: "󰂱 {device}" }
    on:
      left: { cycle: [detail] }
```

## `clock`

Wall-clock date and time.

Shipped defaults:

```yaml
format: "{time:%Y-%m-%d %H:%M:%S}"
tick: 5s
auto_tick: true
on:
  right: calendar
```

| Option | Default | Description |
|---|---|---|
| `auto_tick` | `true` | derive the tick from the displayed granularity: a format showing seconds ticks every second, one showing minutes ticks on the minute |
| `tick` | `5s` | fixed interval, used when `auto_tick` is off |

Ticks are aligned to wall-clock boundaries, so `%M` changes exactly on
the minute.

| Placeholder | Description |
|---|---|
| `{time}` | current time; the spec is a strftime layout, e.g. `{time:%H:%M}` |

| Verb | Effect |
|---|---|
| `calendar` | open the calendar menu at the pointer (shipped: `right`) |

## `cpu`

CPU usage percentage.

Shipped defaults:

```yaml
format: " {cpu}%"
tick: 3s
high_at: 90
high_for: 7s
states:
  high: { fg: "@urgent" }
```

| Option | Default | Description |
|---|---|---|
| `tick` | `3s` | sample interval |
| `high_at` | `90` | usage percentage that arms the `high` state |
| `high_for` | `7s` | how long usage must stay above `high_at` before `high` triggers |

| Placeholder | Description |
|---|---|
| `{cpu}` | usage percentage |

| State | Shipped styling | When |
|---|---|---|
| `high` | `fg: "@urgent"` | sustained load (see `high_at`/`high_for`) |

## `custom`

User-defined text. Combine `format`, states and `on:` for anything
interactive; there is no module-specific behavior.

Shipped defaults:

```yaml
format: ""
```

```yaml
- custom:
    format: "hello"
    states:
      alt: { format: "world" }
    on:
      left: { cycle: [alt] }
      right: { run: "pavucontrol" }
```

No options, placeholders, states or verbs of its own.

## `disk`

Filesystem usage for one mountpoint.

Shipped defaults:

```yaml
format: "{icon} {used_pct}%"
tick: 10s
path: /
icon: disk
unit: auto
use_si: false
warn_at: 80
critical_at: 90
states:
  warn: { fg: "@warning" }
  critical: { fg: "@urgent" }
```

| Option | Default | Description |
|---|---|---|
| `path` | `/` | mountpoint to report |
| `tick` | `10s` | refresh interval |
| `icon` | disk icon | icon used by `{icon}` |
| `unit` | `auto` | `auto` scales dynamically; or a fixed unit (`GB`, `GiB`, `MB`, ...) |
| `use_si` | `false` | decimal (SI) units instead of binary |
| `warn_at` | `80` | percentage that turns on `warn` |
| `critical_at` | `90` | percentage that turns on `critical` |

| Placeholder | Description |
|---|---|
| `{icon}` | module icon |
| `{used}` `{free}` `{total}` | space in the selected unit |
| `{used_pct}` `{free_pct}` | percentages |
| `{unit}` | selected unit name |

| State | Shipped styling | When |
|---|---|---|
| `warn` | `fg: "@warning"` | usage at or above `warn_at` |
| `critical` | `fg: "@urgent"` | usage at or above `critical_at` |

A handy detail toggle:

```yaml
- disk:
    states:
      detail: { format: "{icon} {used:.2f}/{total:.2f} {unit}" }
    on:
      left: { cycle: [detail] }
```

## `idleinhibitor`

Keeps the system awake through the desktop portal.

Shipped defaults:

```yaml
format: "󰛐"
states:
  inhibiting: { format: "󰛑" }
on:
  left: toggle
```

No options or placeholders.

| State | Shipped styling | When |
|---|---|---|
| `inhibiting` | format `󰛑` | idle is currently inhibited |

| Verb | Effect |
|---|---|
| `toggle` | toggle idle inhibition (shipped: `left`) |

## `locale`

Current locale from the environment.

Shipped defaults:

```yaml
format: "{locale}"
tick: 7s
```

| Option | Default | Description |
|---|---|---|
| `tick` | `7s` | refresh interval |

| Placeholder | Description |
|---|---|
| `{locale}` | language-REGION, e.g. `en-US` |

## `mpris`

Media player status via MPRIS.

Shipped defaults:

```yaml
format: "󰫔"
states:
  playing: { format: " {title~} • {artists~}" }
  paused: { format: " {title~} • {artists~}" }
cursor: pointer
on:
  left: play-pause
  right: raise
```

No options.

| Placeholder | Description |
|---|---|
| `{artists}` | track artists, comma separated |
| `{title}` | track title |

| State | Shipped styling | When |
|---|---|---|
| `playing` | play-icon format | a player is playing |
| `paused` | pause-icon format | a player is paused |

The base format (idle icon) shows when no player is active.

| Verb | Effect |
|---|---|
| `play-pause` | toggle playback on the active player (shipped: `left`) |
| `raise` | bring the active player's UI to the front (shipped: `right`) |

## `powerprofiles`

Power profile via power-profiles-daemon.

Shipped defaults:

```yaml
format: "󰐦"
states:
  performance: { format: "󰓅" }
  balanced: { format: "󰾅" }
  power-saver: { format: "󰾆" }
on:
  left: toggle
  right: menu
```

No options.

| Placeholder | Description |
|---|---|
| `{profile}` | active profile name |

| State | Shipped styling | When |
|---|---|---|
| `performance` | format `󰓅` | that profile is active |
| `balanced` | format `󰾅` | |
| `power-saver` | format `󰾆` | |

| Verb | Effect |
|---|---|
| `toggle` | cycle performance, power-saver, balanced (shipped: `left`) |
| `menu` | open the profile menu at the pointer (shipped: `right`) |

## `ram`

Memory usage.

Shipped defaults:

```yaml
format: "{icon} {used_pct}%"
tick: 10s
icon: compass
unit: auto
use_si: false
warn_at: 80
critical_at: 90
states:
  warn: { fg: "@warning" }
  critical: { fg: "@urgent" }
```

Options, placeholders and states are identical to [`disk`](#disk),
minus `path` (and with its own default `icon`).

## `gap`

The punctuation between modules. Written between two entries it overrides
[`bar.gap`](/docs/configuration#bar) at that one join, and `- gap: ""` puts
its neighbours flush together.

Shipped defaults are just a format:

```yaml
format: " "
```

A scalar entry sets the format, so a bar's punctuation stays one line each,
and block keys style it like any other module:

```yaml
- gap: ""                                # neighbours flush together
- gap: "   "                             # wider than bar.gap, here only
- gap: " · "                             # a divider
- gap: { format: " │ ", fg: "@cool" }    # a styled divider
```

An automatic `bar.gap` is never inserted next to a `gap` entry, so writing
one replaces it at that join rather than doubling up on it.

`gap` replaces the old `sep` and `space` modules, which were the same module
with different default text: write `- gap: " │ "` for the former and
`- gap: " "` for the latter.

## `sessioncontrols`

Session menu launcher.

Shipped defaults:

```yaml
format: "󰐦"
on:
  right: menu
```

| Verb | Effect |
|---|---|
| `menu` | open the session menu: lock, logout, shutdown, ... (shipped: `right`) |

## `title`

Focused window title (hyprland, i3/sway).

Shipped defaults:

```yaml
format: "{title~}"
states:
  class: { format: " {class} ", fg: "@black", bg: "@cool" }
```

No options.

| Placeholder | Description |
|---|---|
| `{title}` | focused window title |
| `{class}` | focused window class |

| State | Shipped styling | When |
|---|---|---|
| `class` | chip styling (see above) | per-segment state on the window-class chip rendered before the title |

## `tray`

Status-notifier item tray.

Shipped defaults:

```yaml
cursor: pointer
```

No options, placeholders or verbs. Left click activates an item, middle
click secondary-activates, right click opens its menu, scrolling
scrolls it.

## `volume`

Default-sink volume via pulseaudio/pipewire.

Shipped defaults:

```yaml
format: "{icon} {vol}%"
icons: ["󰕿", "󰖀", "󰕾"]
step: 5
states:
  muted: { fg: darkgray, format: "󰖁 MUTED" }
on:
  left: toggle-mute
  scroll-up: volume-up
  scroll-down: volume-down
```

| Option | Default | Description |
|---|---|---|
| `icons` | `["󰕿", "󰖀", "󰕾"]` | icon ramp, picked by volume |
| `step` | `5` | percentage step for `volume-up`/`volume-down` |

| Placeholder | Description |
|---|---|
| `{icon}` | volume level icon |
| `{vol}` | volume percentage |

| State | Shipped styling | When |
|---|---|---|
| `muted` | `fg: darkgray`, format `󰖁 MUTED` | the default sink is muted |

| Verb | Effect |
|---|---|
| `toggle-mute` | mute/unmute (shipped: `left`) |
| `volume-up` | raise by `step` (shipped: `scroll-up`) |
| `volume-down` | lower by `step` (shipped: `scroll-down`) |

## `wifi`

Wifi status via NetworkManager.

Shipped defaults:

```yaml
format: "{icon}"
tick: 5s
device_index: 2
icons: ["󰤯", "󰤟", "󰤢", "󰤥", "󰤨"]
states:
  disconnected: { fg: darkgray, format: "󰤭" }
```

| Option | Default | Description |
|---|---|---|
| `tick` | `5s` | signal strength refresh interval |
| `device_index` | `2` | NetworkManager device number |
| `icons` | `["󰤯", ... "󰤨"]` | icon ramp, picked by strength |

| Placeholder | Description |
|---|---|
| `{icon}` | signal strength icon |
| `{ssid}` | connected network name |
| `{interface}` | wireless interface name |
| `{strength}` | signal strength percentage |

| State | Shipped styling | When |
|---|---|---|
| `disconnected` | `fg: darkgray`, format `󰤭` | no active access point |

A detail toggle plus interface on hover:

```yaml
- wifi:
    states:
      detail: { format: "{icon} {ssid}" }
      hover: { format: "{interface}" }
    on:
      left: { cycle: [detail] }
```

## `ws`

Workspaces (hyprland, i3/sway). Clicking a workspace switches to it
(the shipped `left: goto` binding; unbind with `left: ~`).

Shipped defaults:

```yaml
format: " {ws} "
cursor: pointer
current_only: false
states:
  urgent: { fg: "@black", bg: "@urgent" }
  active: { fg: "@black", bg: "@active" }
  special: { fg: "@active", bg: "@special" }
on:
  left: goto
```

| Option | Default | Description |
|---|---|---|
| `current_only` | `false` | render only the focused workspace |

| Placeholder | Description |
|---|---|
| `{ws}` | workspace name |

| State | Shipped styling | When |
|---|---|---|
| `urgent` | `fg: "@black", bg: "@urgent"` | workspace with an urgent window (applied per workspace segment) |
| `active` | `fg: "@black", bg: "@active"` | the focused workspace (per segment) |
| `special` | `fg: "@active", bg: "@special"` | special/scratchpad workspace (per segment) |

| Verb | Effect |
|---|---|
| `goto` | switch to the clicked workspace (shipped: `left`) |

`current_only` is a plain option, so a user state can override it and a
binding can toggle that state; this shows only the focused workspace
after a right click:

```yaml
- ws:
    states:
      focus: { current_only: true }
    on:
      right: { cycle: [focus] }
```
