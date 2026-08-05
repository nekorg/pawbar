# pawbar
A kitten-panel based desktop panel for your desktop

![image](https://github.com/user-attachments/assets/b8cdfd44-ca66-45df-a8eb-d8142d0e4ffb)

## Why?
Due to the existence of modern terminal standards (especially in kitty), a tiny terminal windows support true colors, [text-sizing](https://sw.kovidgoyal.net/kitty/text-sizing-protocol/), [images](https://sw.kovidgoyal.net/kitty/graphics-protocol/), mouse support (clicking, dragging, hover and scrolling), etc. Which makes it a viable option for a status bar, instead of a GUI Toolkits (like GTK). Kitty enables this using its [panel](https://sw.kovidgoyal.net/kitty/kittens/panel/) mode/kitten, through which it offers all the customization/capabilities of a kitty terminal window but as a status bar on top of your screen. 


## Installing
> [!IMPORTANT]
> You need to checkout the submodules before building `pawbar`, you can do one of the following:
> - Clone the repository with
>   ```
>   git clone --recurse-submodules https://github.com/codelif/pawbar.git
>   cd pawbar
>   ```
> 
> - Clone normally and checkout manually:
>   ```
>   git clone https://github.com/codelif/pawbar.git
>   cd pawbar
>   git submodule update --remote --init
>   ```
A basic install script is there (you need to compile pawbar before running the script): 
```sh
go build .
./install.sh
```
Though fair caution it installs to `/usr/local/bin`

> [!NOTE]
> I will add other installation methods when I am satisfied with this project.
## Usage
Run bar by calling the bar script after using the `install.sh` script:
```sh
pawbar
```


One `pawbar` covers the whole desktop: it puts a bar on every monitor, pinned to it, and follows monitor hotplug. Restrict it with `bar.outputs` (or `pawbar --output NAME`), and tailor a single screen's bar under a top-level `outputs:` section.

You can add modules by editing `$HOME/.config/pawbar/pawbar.yaml`. The config is hot-reloaded, so the bar updates as you save. Validate it any time with `pawbar --check`. See [docs/examples/pawbar.yaml](docs/examples/pawbar.yaml) for a starting point and the docs for the full schema (theme variables, per-state styling, format placeholders and mouse bindings).

It has 18 modules (all customisable upto a certain extent,for now):
 - `backlight`: A screen brightness indicator (interactable)
 - `battery`: A battery module with dynamic icons and colors
 - `bluetooth`: A simple bluetooth conenction indicator (for now, without interactive menu)
 - `clock`: A simple date-time module (format changable on click, includes timeline-wide calendar)
 - `cpu`: CPU usage 
 - `custom`: perform custom tasks, e.g. running at script, opening an app etc.
 - `disk`: Disk usage (format changable on click)
 - `idleInhibitor`: toggle screen off/lock actions as allowed/inhibited.
 - `locale`: Current locale
 - `mpris`: mpris player, with play/pause (interactable),artist,title.
 - `powerprofiles`: An interactable menu to choose Performance,Balanced,Power-saver mode from.
 - `ram`: RAM usage (format changable on click)
 - `title`: A window class & title display (hyprland/i3/sway)
 - `sessioncontrols`: An interactable menu to choose Reboot,PowerOff,Suspend,Logout from.
 - `tray`: tray using nm-applet (with menu)
 - `volume`: A Volume level indicator (interactable)
 - `wifi`: A simple wifi conenction indicator (without menu, interatable on clicks)
 - `ws`: A dynamic workspace switcher (hyprland/i3/sway) with (with mouse events) (change workspace on click), showing its own monitor's workspaces

 - `gap`: The punctuation between modules: a space by default, `- gap: ""` to
   join two modules flush, `- gap: " │ "` for a divider. `bar.gap` sets the
   default for every join, so most configs need no entries at all.

A typical config looks like:
```yaml
bar:
  gap: " "                # breathing room between every pair of modules

theme:
  defaults:
    states:
      hover: { bold: true }

left:
  - ws
  - title

right:
  - volume:
      states:
        muted: { fg: "@warning" }
  - gap: " │ "            # override bar.gap at this one join
  - battery
  - gap: " │ "
  - clock:
      format: "{time:%H:%M}"
      states:
        full: { format: "{time:%A %d %B %H:%M:%S}" }
      on:
        left: { cycle: [full] }
```

## Roadmap
 - [x] Running
 - [ ] Modules and Services:
     - [ ] bluetooth (with menu)
     - [ ] tray (more functional)
     - [ ] workspace and title for more WMs
     - [ ] Suggest more
 - [ ] Extended module config
 - [ ] Extended bar config
 - [ ] Menu support

## Contribution
Project is in very early stages, any contribution is very much appreciated. 
