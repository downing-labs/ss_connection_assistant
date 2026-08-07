# SteelSeries Connection Assistant

Developed by Damon Downing, 2026

A lightweight Windows tray watchdog that fixes a specific SteelSeries
Sonar bug: when a second audio device (e.g. a Bluetooth speaker)
connects, Sonar silently reassigns the `game`/`chat`/`media`/`aux`
channels away from your headset to the new device — no crash, no error,
just silent rerouting. This breaks the physical ChatMix dial and misroutes
audio until you manually notice and fix it in GG.

This app watches for that specific drift via Sonar's own local REST API
(the same one GG's UI uses) and re-applies the fix automatically —
functionally identical to clicking "Switch it" in GG, just automatic.

## Features

- **Tray icon** — green (all correct), amber (Sonar unreachable or audio
  misrouted), purple (Work Mode — monitoring paused)
- **Game Mode / Work Mode** — toggle via tray menu. Work Mode pauses all
  monitoring and fixing (and sends one final mic-unmute so you don't
  carry a mute into a work call)
- **Auto-fix** — checks every 5 seconds in Game Mode; fixes drift the
  moment it's detected
- **Pause/Break hotkey** — global hotkey (only active in Game Mode) to
  toggle mic mute instantly, without alt-tabbing anywhere
- **Reset** — manually re-check and fix immediately
- **Restart Sonar** — kills and lets GG's own supervisor relaunch
  `SteelSeriesSonar.exe`, for the rare case Sonar itself needs a kick

## How it works

No HID, no Windows Core Audio COM calls — just plain HTTP against
Sonar's local REST API (port discovered dynamically each run, since it
changes on every Sonar restart). See `internal/sonarapi` for the client.

## Building

Requires Go 1.26+. No C compiler / cgo needed — everything here is
pure Go plus Windows syscalls.

**Quick dev run** (console window, live log output):

```powershell
go run .
```

**Portable release build** (no console window, headphone icon on the
.exe itself):

```powershell
go build -ldflags="-H=windowsgui" -o "SteelSeries Connection Assistant.exe" .
```

The `.ico` used for the compiled `.exe`'s own icon (Explorer, taskbar,
Alt+Tab — separate from the live tray icon, which changes color with
state) is generated from the same headphone glyph and already committed
as `rsrc_windows_amd64.syso` / `rsrc_windows_386.syso`, which Go links
in automatically. You only need to regenerate these if you change the
glyph or the icon colors:

```powershell
go run ./cmd/geticon                          # writes app.ico
go install github.com/tc-hib/go-winres@latest # one-time tool install
go-winres simply --icon app.ico               # regenerates the .syso files
```

## Credits

- Headphone icon: [Headphones icons created by Magnific - Flaticon](https://www.flaticon.com/free-icons/headphones)

## Support

If this saved you some annoyance, consider supporting development:
https://ko-fi.com/hackpig1974
