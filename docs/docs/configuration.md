---
next:
  text: 'Modules'
  link: '/docs/modules'
---
# Configuration

pawbar reads `~/.config/pawbar/pawbar.yaml` (override with `PAWBAR_CONFIG` or
`--config`). The file is hot-reloaded: save it and the bar updates in place.

A config has five top-level sections, all optional:

```yaml
bar:      # bar-global settings
theme:    # variables and bar-wide default styling
left:     # modules anchored left
middle:   # modules centered
right:    # modules anchored right
```

Every problem in the config is reported with its file position and a
"did you mean" hint. By default a broken module entry renders as an error
chip (`⚠name`) while the rest of the bar runs; set `bar.strict: true`
(or pass `--strict`) to refuse to start instead. `pawbar --check`
validates the config and exits; `pawbar --resolved` prints the fully
merged per-slot configuration for cascade debugging.

# `bar`

```yaml
bar:
  gap: " "
  shrink_min: 3
  truncate_priority: [right, left, middle]
  enable_ellipsis: true
  ellipsis: "…"
  strict: false
  defaults: true
```

- `gap`: inserted between adjacent modules on a side, so you don't have to
  spell one out between every pair. Empty (the default) keeps modules flush.
  Automatic gaps are layout rather than modules: they are never added at a
  side's edge, never next to an explicit [`gap` entry](#separators-and-gaps),
  and they take `theme.defaults` styling; for anything else, write the join
  out.
- `shrink_min`: the floor, in columns, that an
  [elastic placeholder](#elastic-text) is never shrunk below.
- `truncate_priority`: which anchors keep their content when the bar
  overflows; earlier wins. Must list all three.
- `enable_ellipsis` / `ellipsis`: mark truncation points.
- `strict`: any config issue aborts startup (and rejects hot reloads).
- `defaults`: set `false` to drop every module's
  [shipped defaults](#shipped-defaults) bar-wide.

# `theme`

```yaml
theme:
  vars:
    accent: "#7aa2f7"
    warn: orange
  defaults:
    fg: "@accent"
    bold: false
    states:
      hover: { bold: true }     # applies to every module's hover
```

- `vars`: named values. Reference them anywhere with `@name`. Built-in
  color names (`@urgent`, `@good`, `@color112`, ...) still work where your
  vars don't shadow them.
- `defaults`: a [block](#blocks-style--format) applied to every module,
  plus per-state blocks under `states:`.

# Modules

Each side is a list. An entry is a bare name, a `name: {options}` mapping,
or `name: "text"`: a scalar is shorthand for setting just `format`:

```yaml
left:
  - ws
  - gap: " │ "
middle:
  - clock:
      format: "{time:%H:%M}"
right:
  - volume
```

## Separators and gaps

[`bar.gap`](#bar) handles the usual case: uniform breathing room between
every pair of modules, with nothing to write per entry. Where one join
should differ, write a `gap` entry there and it wins:

```yaml
right:
  - cpu
  - gap: ""          # cpu and ram flush together
  - ram
  - gap: " │ "       # a divider instead of the usual gap
  - clock
```

An automatic gap is never inserted next to an explicit one, so entries
replace `bar.gap` at that join rather than doubling up on it. `gap` takes
block keys like any module (`- gap: { format: " │ ", fg: "@dim" }`). It
replaces the old `sep` and `space` modules: write `- gap: " │ "` and
`- gap: " "`.

A module that has nothing to show (mpris with no player, tray with no
icons) takes up no room, and a separator left facing only empty modules is
dropped with it, so you never get a stranded `│` floating at the end of a
side. A separator at the very edge of a side is kept: it divides that side
from the rest of the bar rather than from a neighbour.

## Shipped defaults

A module's default configuration is not baked into code: every module
ships a small yaml file, in exactly this config syntax, that forms the
bottom layer of its cascade. A bare `- ws` entry gets exactly the
contents of that file; nothing more. Empty config means an empty bar,
and everything the bar does is written down somewhere you can read:

```sh
pawbar defaults            # list modules
pawbar defaults ws         # print ws's shipped defaults verbatim
```

You control the whole layer:

```yaml
- ws:
    on:
      left: ~              # unbind a shipped binding: null removes it
    states:
      active: ~            # drop a shipped state's styling entirely

- clock: { defaults: false }   # start this entry from a blank slate
```

With `defaults: false` (per entry, or bar-wide under `bar:`) nothing is
inherited and the entry becomes fully manual: a module that renders
placeholders requires an explicit `format`, and every option the module
declares (`tick`, `warn_at`, ...) must be set. Anything missing is a
config error listing exactly which keys are absent.

## Blocks (style + format)

A *block* is the styling surface every module shares. All keys are
optional; unset keys inherit from the layer below.

| key | meaning |
|---|---|
| `fg`, `bg` | colors: CSS names, `#hex`, `rgb(r,g,b)`, `@var` |
| `bold`, `dim`, `italic`, `underline`, `blink`, `reverse`, `strikethrough` | booleans |
| `cursor` | pointer shape while hovering ([CSS cursor names](https://developer.mozilla.org/en-US/docs/Web/CSS/cursor)) |
| `format` | placeholder format string (see below), or a list of them from widest to most [compact](#compact-formats) |
| `template` | opt-in Go `text/template` alternative to `format` |

`priority` sits alongside the block keys but is not one: it is per entry
rather than per state, and orders which modules go
[compact](#compact-formats) first.

Block keys go directly at the top level of a module entry:

```yaml
- clock:
    fg: "@accent"
    format: "{time:%H:%M}"
```

## Format strings

`format` uses placeholders: `{name}` inserts a value the module
provides, `{name:spec}` formats it. Unknown placeholder names are config
errors, so typos are caught at load time.

- Time values take a strftime layout: `{time:%A %d %B}`.
- Numbers take a printf spec without the `%`: `{vol:3}` pads to 3,
  `{load:.2f}` renders two decimals.
- `~` marks a value as [elastic](#elastic-text): `{title~}`.
- <code>&#123;&#123;</code> and <code>&#125;&#125;</code> are literal braces.

Each module's placeholders are listed in the [module reference](/docs/modules).

Power users can set `template` instead: a Go `text/template` over the
same values (<code>&#123;&#123;.vol&#125;&#125;</code>), with `round`, `strftime` and `shrink` helper
functions. `format` and `template` are mutually exclusive per block.

## Elastic text

When the bar runs out of room something has to give, and by default it is
whatever happens to sit at the end being trimmed away, which is rarely
what you want. Mark the parts that *should* give way with `~`:

```yaml
- mpris:
    format: "{icon} {title~} • {artists~}"
```

Now the icon and the `•` are untouchable, and the title and artist shrink
instead. They shrink *fairly*: the longer one gives way until the two are
the same length, then they shorten together. Weights bias that split:
`{title~2} • {artists~1}` keeps twice as many columns for the title.

The floor is `bar.shrink_min` columns; nothing shrinks past it, and a
specifier still applies (`{title~2:.60s}`).

Each anchor is fitted into the room its position actually leaves it, in
`bar.truncate_priority` order: the anchor listed first keeps its content,
and the ones after it shrink into what remains. Note that a middle module
is *centered*, so it splits the bar in half: with a clock in the middle,
the right side can only use the columns past it, however empty the left
half is.

If shrinking alone cannot close the gap, modules start
[stepping down](#compact-formats); if that runs out too, the bar falls back
to trimming blocks by `bar.truncate_priority` as before.

In a `template`, wrap the value in `shrink` instead:

```yaml
template: '{{shrink .title}} • {{shrink 2 .artists}}'
```

`shrink` marks its output, so a pipeline stage that rewrites that output
(<code>&#123;&#123;shrink .title | printf "%q"&#125;&#125;</code>) loses the
marking and the value becomes ordinary rigid text. Appending after it is
fine.

## Compact formats

Shrinking only makes text shorter. When a module would rather *drop* part
of itself than have everything squeezed, give `format` a list, widest
first:

```yaml
- battery:
    format:
      - "{icon} {bat}% ({time})"
      - "{icon} {bat}%"
      - "{icon}"
```

The bar shows the first entry that fits and steps down only as far as it
has to. Nothing is dropped while any elastic text still has room to give,
so `~` and a format list combine cleanly: text shortens first, structure
goes last.

When several modules could step down, the one with the lowest `priority`
goes first; ties break toward the middle of the bar, so the modules at the
outer edges keep their detail longest.

```yaml
- cpu: { priority: -1 }     # first to go compact
- clock: { priority: 1 }    # last
```

`priority` defaults to `0` and can be set in a module's shipped defaults
too. Levels are recomputed from scratch on every redraw, so widening the
bar back out restores whatever a narrower one gave up.

This pairs naturally with `hover`, which re-expands a module under the
pointer no matter how narrow the bar is:

```yaml
- battery:
    format: ["{icon} {bat}%", "{icon}"]
    states:
      hover: { format: "{icon} {bat}% ({time})" }
```

`template` accepts a list in exactly the same way.

## States

Modules expose named *condition states* they turn on and off themselves
(`muted`, `charging`, `high`, ...). A state carries a block that overrides
the base styling while active:

```yaml
- volume:
    format: "{icon} {vol}%"
    states:
      muted: { fg: "@warn", format: "{icon} --" }
```

States can also override module options, not just styling. A state that
sets `tick: 1m` on the clock changes the refresh rate while active.

You can also invent *user states* and toggle them from mouse bindings;
that's how "click to switch format" works:

```yaml
- clock:
    format: "{time:%H:%M}"
    states:
      full: { format: "{time:%A %d %B %H:%M:%S}" }
    on:
      left: { cycle: [full] }
```

Two built-in states always exist: `hover` (pointer over the module) and
`pressed` (button held).

### Merge priority

Low to high; later layers override earlier ones per key:

1. the module's [shipped defaults](#shipped-defaults) file
2. `theme.defaults`
3. the entry's top-level block keys
4. active states, in order: condition states (module declaration order),
   user states, `hover`, `pressed`. Each state's block is itself merged
   from the shipped defaults' `states.<s>`, then
   `theme.defaults.states.<s>`, then the entry's `states.<s>`.

## `on:` mouse bindings

```yaml
on:
  left: toggle-mute              # a module verb (shorthand)
  right: { run: "pavucontrol" }
  middle: { notify: "hello" }
  scroll-up: volume-up
  hover: { set: expanded }       # state held while hovering
```

Buttons: `left`, `right`, `middle`, `scroll-up`, `scroll-down`,
`scroll-left`, `scroll-right`, plus `hover`. A binding is one action or
a list of actions:

- `verb`: invoke a named action the module implements (bare strings are
  verb shorthand). Unknown verb names are config errors.
- `run`: spawn a command (string or argv list).
- `notify`: send a desktop notification.
- `set`: toggle a user state.
- `cycle`: cycle through user states: none, first, ..., last, none.

Shipped defaults may carry bindings (volume's scroll adjusts volume,
clock's right-click opens the calendar); they are plain `on:` entries in
the module's defaults file, visible via `pawbar defaults <name>`. An
`on:` key for the same button replaces the shipped one, and binding a
button to `~` (null) removes it.

# Hot reload

Editing the config applies it live:

- unchanged entries keep running untouched,
- a changed entry is reconfigured in place when the module supports it,
  restarted otherwise,
- theme changes restyle everything without restarts,
- a file that fails to parse is ignored (last good config stays) and the
  error is logged.

Reordering entries restarts the moved modules; diffing is positional.
