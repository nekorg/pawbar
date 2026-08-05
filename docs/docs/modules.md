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

Screen brightness: sysfs and logind for internal panels, DDC/CI over I2C for
external monitors.

Shipped defaults:

```yaml
format: "{icon} {light}%"
icons: ["󰃞", "󰃟", "󰃝", "󰃠"]
backend: auto
monitor: self
step: 5
poll: 10s
on:
  scroll-up: brightness-up
  scroll-down: brightness-down
```

| Option | Default | Description |
|---|---|---|
| `icons` | `["󰃞", "󰃟", "󰃝", "󰃠"]` | icon ramp, picked by brightness level |
| `backend` | `auto` | `auto`, `sysfs` or `ddc` |
| `monitor` | `self` | which output to control: `self` (the one this bar is on) or a connector name like `DP-1` |
| `step` | `5` | percentage step for `brightness-up`/`brightness-down` |
| `poll` | `10s` | how often to re-read a DDC monitor; `0s` disables it |

| Placeholder | Description |
|---|---|
| `{icon}` | brightness level icon |
| `{light}` | brightness percentage |
| `{now}` | raw brightness value |
| `{max}` | raw maximum brightness |
| `{backend}` | resolved backend, `sysfs` or `ddc` |

| Verb | Effect |
|---|---|
| `brightness-up` | raise by `step` (shipped: `scroll-up`) |
| `brightness-down` | lower by `step` (shipped: `scroll-down`) |
| `set-brightness` | set to the percentage given as an argument, e.g. `{ middle: set-brightness 100 }` |

### Choosing a backend

`auto` decides per monitor, and gets it right on ordinary hardware without
any configuration:

1. If the kernel says a backlight device drives this bar's output, use it.
   Laptop panels take this path.
2. Otherwise, if the output is connected and reachable over DDC/CI, use that.
   External monitors take this path.
3. Otherwise fall back to whatever backlight device exists.

Step 1 comes first on purpose. A laptop panel usually exposes an I2C channel
too — its DisplayPort AUX line — and talking DDC/CI to it does nothing good,
so an attached backlight device always wins.

Set `backend` explicitly to skip the guessing: `sysfs` never touches I2C, and
`ddc` fails loudly rather than falling back. To vary it per monitor, put the
entry under a top-level `outputs:` section:

```yaml
outputs:
  DP-1:
    right: [{ backlight: { backend: ddc } }, clock]
```

`backend`, `monitor` and `poll` are settled when the module starts; overriding
them from a `states:` block has no effect. The rest are live.

A monitor changed with its own OSD buttons announces nothing, so `poll` is how
that gets noticed. Internal panels do emit udev events and ignore `poll`.

### Permissions

Internal panels need no setup. pawbar asks logind to write the brightness,
which any active session may do — no group, no udev rule, no polkit prompt.
On a system without logind it writes `/sys/class/backlight/*/brightness`
directly, which needs membership in `video`, and falls back to `brightnessctl`
if that is installed.

DDC/CI is the part that needs permissions, because it means reading and
writing `/dev/i2c-*`.

1. Load the kernel module:

   ```sh
   sudo modprobe i2c-dev
   echo i2c-dev | sudo tee /etc/modules-load.d/i2c-dev.conf
   ```

2. Grant access, either way:

   - **Easiest — install `ddcutil` (2.0 or newer).** Its udev rules tag
     display I2C devices with `uaccess`, so the logged-in user gets an ACL
     automatically, with no group to join and no logout. Check it took with
     `getfacl /dev/i2c-*`.
   - **Manual.** Create the group, join it, and add a rule:

     ```sh
     sudo groupadd -f i2c
     sudo usermod -aG i2c $USER
     echo 'KERNEL=="i2c-[0-9]*", GROUP="i2c", MODE="0660"' \
       | sudo tee /etc/udev/rules.d/45-pawbar-i2c.rules
     sudo udevadm control --reload && sudo udevadm trigger
     ```

     Then log out and back in, so your session picks up the new group.

3. Confirm your monitors answer: `ddcutil detect`.

**Optional: install `ddcutil-service`.** pawbar prefers it when it is
present. It is a resident D-Bus service, so pawbar never spawns a process to
change brightness, and it carries libddcutil's quirk handling for monitors
that bend the spec. It runs as your user, so it does not replace the
`/dev/i2c-*` access above. Without it pawbar speaks DDC/CI on the bus itself.

**Nvidia's proprietary driver** often fails at DDC/CI, and the I2C buses it
exposes are frequently not the DDC channels at all. If `ddcutil detect` finds
nothing, `auto` will quietly fall back to sysfs; `backend: sysfs` skips the
probe entirely.

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
monitor: self
states:
  class: { format: " {class} ", fg: "@black", bg: "@cool" }
```

| Option | Default | Description |
|---|---|---|
| `monitor` | `self` | which window to follow: `self` (the one this bar's monitor is showing) or `focused` (wherever focus is) |

| Placeholder | Description |
|---|---|
| `{title}` | focused window title |
| `{class}` | focused window class |

With `monitor: self` each bar names the window on its own screen, so the
title stays put when you move focus to the other monitor; `focused` makes
every bar mirror the focused window, as pawbar did before.

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
monitor: self
states:
  urgent: { fg: "@black", bg: "@urgent" }
  active: { fg: "@black", bg: "@active" }
  visible: { fg: "@active" }
  special: { fg: "@active", bg: "@special" }
on:
  left: goto
```

| Option | Default | Description |
|---|---|---|
| `current_only` | `false` | render only what is on screen |
| `monitor` | `self` | whose workspaces to show: `self`, `all`, or an output name |

| Placeholder | Description |
|---|---|
| `{ws}` | workspace name |

| State | Shipped styling | When |
|---|---|---|
| `urgent` | `fg: "@black", bg: "@urgent"` | workspace with an urgent window (applied per workspace segment) |
| `active` | `fg: "@black", bg: "@active"` | the focused workspace (per segment) |
| `visible` | `fg: "@active"` | on screen on its monitor, but without focus (per segment) |
| `special` | `fg: "@active", bg: "@special"` | special/scratchpad workspace (per segment) |

| Verb | Effect |
|---|---|
| `goto` | switch to the clicked workspace (shipped: `left`) |

### Multiple monitors

Each bar shows its own monitor's workspaces (`monitor: self`), which is
what a single-monitor setup was doing all along. Set `monitor: all` to
show every workspace on every bar, or name an output to pin a bar's list
to it:

```yaml
- ws:
    monitor: all
```

Exactly one workspace in the session is `active` — the one with keyboard
focus. The workspace displayed on each *other* monitor is `visible`, so
the bar on the screen you are not typing on still marks where you are.

`goto` focuses the workspace's monitor as well as the workspace, so
clicking the bar on your second screen moves you there even when the
workspace is empty.

Special/scratchpad workspaces are hyprland-only: sway keeps its
scratchpad out of the workspace list, so nothing there is ever `special`.

`current_only` is a plain option, so a user state can override it and a
binding can toggle that state; this shows only what is on screen after a
right click:

```yaml
- ws:
    states:
      focus: { current_only: true }
    on:
      right: { cycle: [focus] }
```
