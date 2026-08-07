//go:build windows

// Package sonarapi talks to SteelSeries Sonar's local REST API — the
// same one GG's own UI uses. Discovered and confirmed by hand against a
// real installation:
//   - Sonar listens on a port that changes every time it restarts.
//   - GET  /audioDevices          — every Windows audio endpoint Sonar
//                                    knows about, with friendlyName/id.
//   - GET  /classicRedirections   — which physical device backs each of
//                                    the game/chat/media/aux/mic virtual
//                                    channels right now.
//   - PUT  /classicRedirections/{channel}/deviceId/{deviceId}
//                                    — reassign a channel to a device.
//     This is exactly what GG's own "Switch it" button does.
//
// Root cause this whole package exists to fix: when a second audio
// device (e.g. a Bluetooth speaker) connects, Sonar silently reassigns
// game/chat/media/aux away from the headset to the new device — no
// crash, no error, just silent rerouting. That's what breaks the
// physical ChatMix dial.
package sonarapi

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// DiscoverPort finds the TCP port SteelSeriesSonar.exe is currently
// listening on. Uses the same technique confirmed working by hand:
// match a LISTEN-state TCP connection to its owning process name.
//
// Shells out to PowerShell rather than using raw Windows syscalls —
// deliberately, after the HID enumeration work in the earlier version
// of this project ran into real heap corruption from hand-rolled native
// calls. This is slower (~100-300ms per call) but far lower-risk, and
// discovery only needs to run occasionally (cached, re-run only on
// connection failure), not in a hot loop.
func DiscoverPort() (int, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`(Get-NetTCPConnection | Where-Object { $_.State -eq "Listen" } | `+
			`Select-Object @{Name="ProcName";Expression={(Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).ProcessName}}, LocalPort | `+
			`Where-Object { $_.ProcName -eq "SteelSeriesSonar" }).LocalPort`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("running port discovery: %w", err)
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return 0, fmt.Errorf("SteelSeriesSonar.exe not found listening on any port (is Sonar running?)")
	}

	// If Sonar has more than one listener, PowerShell prints one per
	// line — take the first.
	firstLine := strings.SplitN(text, "\n", 2)[0]
	port, err := strconv.Atoi(strings.TrimSpace(firstLine))
	if err != nil {
		return 0, fmt.Errorf("parsing port from %q: %w", text, err)
	}
	return port, nil
}
