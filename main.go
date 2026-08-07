// SteelSeries Connection Assistant — tray watchdog.
//
// Watches Sonar's local REST API for the specific bug we confirmed by
// hand: when a second audio device connects (e.g. a Bluetooth speaker),
// Sonar silently reassigns game/chat/media/aux away from the headset.
// This re-applies the same fix GG's own "Switch it" button performs,
// automatically, whenever that drift is detected.
package main

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"steelseries-connection-assistant/internal/hotkey"
	"steelseries-connection-assistant/internal/sonarapi"
	"steelseries-connection-assistant/internal/tray"
)

const kofiURL = "https://ko-fi.com/hackpig1974"

func main() {
	app := tray.New()
	app.Run(func(a *tray.App) {
		go runWatchdog(a)
	})
	fmt.Println("INFO: Tray exited.")
}

// watchdog holds mutable state shared between the select loop and the
// check/fix helpers. Protected by mu since RestartSonar and the ticker
// path can both touch it.
type watchdog struct {
	mu       sync.Mutex
	mode     tray.Mode
	port     int // cached Sonar port; 0 means "unknown, rediscover"
	micMuted bool
}

func runWatchdog(a *tray.App) {
	w := &watchdog{mode: tray.ModeGame}

	var hk *hotkey.Listener
	var hkPressed chan struct{} // nil (blocks forever in select) until a hotkey listener is running

	startHotkey := func() {
		if hk != nil {
			return
		}
		listener, err := hotkey.Start()
		if err != nil {
			log.Printf("WARN: failed to register Pause/Break hotkey: %v", err)
			return
		}
		hk = listener
		hkPressed = listener.Pressed
		fmt.Println("INFO: Mic-mute hotkey (Pause/Break) active.")
	}
	stopHotkey := func() {
		if hk == nil {
			return
		}
		hk.Stop()
		hk = nil
		hkPressed = nil
		fmt.Println("INFO: Mic-mute hotkey stopped.")
	}
	startHotkey() // Game Mode is the default starting mode
	defer stopHotkey()

	checkNow := make(chan struct{}, 1)
	trigger := func() {
		select {
		case checkNow <- struct{}{}:
		default:
		}
	}
	trigger() // run one check immediately on startup

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case m := <-a.ModeCh:
			w.mu.Lock()
			w.mode = m
			w.mu.Unlock()
			if m == tray.ModeWork {
				fmt.Println("INFO: Switched to Work Mode — monitoring paused.")
				stopHotkey()
				unmuteBeforeWork(a, w)
				a.SetState(tray.StateWorkMode, "Work Mode — monitoring paused")
			} else {
				fmt.Println("INFO: Switched to Game Mode — checking now.")
				startHotkey()
				trigger()
			}

		case <-hkPressed:
			toggleMic(a, w)

		case <-a.ResetCh:
			w.mu.Lock()
			mode := w.mode
			w.mu.Unlock()
			if mode == tray.ModeWork {
				fmt.Println("INFO: Reset ignored — currently in Work Mode.")
				continue
			}
			trigger()

		case <-a.RestartSonarCh:
			restartSonar(a, w)

		case <-a.AboutCh:
			showAbout()

		case <-a.KofiCh:
			openURL(kofiURL)

		case <-checkNow:
			runCheckAndFix(a, w)

		case <-ticker.C:
			w.mu.Lock()
			mode := w.mode
			w.mu.Unlock()
			if mode == tray.ModeGame {
				runCheckAndFix(a, w)
			}
		}
	}
}

// runCheckAndFix does one full cycle: discover port (cached), read
// devices + redirections, and re-apply the headset device ID to any
// output channel that's drifted away from it.
func runCheckAndFix(a *tray.App, w *watchdog) {
	port, err := ensurePort(w)
	if err != nil {
		log.Printf("ERROR: %v", err)
		a.SetState(tray.StateProblem, "Sonar: Unreachable")
		return
	}

	client := sonarapi.NewClient(port)

	devices, err := client.GetAudioDevices()
	if err != nil {
		log.Printf("ERROR: %v", err)
		w.mu.Lock()
		w.port = 0 // force rediscovery next time — port may have changed
		w.mu.Unlock()
		a.SetState(tray.StateProblem, "Sonar: Unreachable")
		return
	}

	headsetID, err := sonarapi.FindHeadsetDeviceID(devices)
	if err != nil {
		log.Printf("ERROR: %v", err)
		a.SetState(tray.StateProblem, "Headset not found in Sonar's device list")
		return
	}

	redirections, err := client.GetClassicRedirections()
	if err != nil {
		log.Printf("ERROR: %v", err)
		a.SetState(tray.StateProblem, "Sonar: Unreachable")
		return
	}

	var mismatched []string
	for _, r := range redirections {
		if !isOutputChannel(r.ID) {
			continue
		}
		if r.DeviceID != headsetID {
			mismatched = append(mismatched, r.ID)
		}
	}

	if len(mismatched) == 0 {
		fmt.Println("INFO: Audio routing OK.")
		a.SetState(tray.StateOK, "Sonar: Connected | Audio: OK")
		return
	}

	fmt.Printf("INFO: Mismatch on %v — fixing...\n", mismatched)
	a.SetState(tray.StateProblem, "Sonar: Connected | Audio: Mismatched — fixing...")
	fixFailed := false
	for _, ch := range mismatched {
		if err := client.SetClassicRedirection(ch, headsetID); err != nil {
			log.Printf("WARN: failed to fix %s: %v", ch, err)
			fixFailed = true
		}
	}

	if fixFailed {
		a.SetState(tray.StateProblem, "Sonar: Connected | Audio: fix failed")
	} else {
		fmt.Println("INFO: Fixed.")
		a.SetState(tray.StateOK, "Sonar: Connected | Audio: OK (just fixed)")
	}
}

func isOutputChannel(id string) bool {
	for _, ch := range sonarapi.OutputChannels {
		if id == ch {
			return true
		}
	}
	return false
}

// toggleMic reads the current chatCapture mute state and flips it,
// updating the tray tooltip (not the icon color — mute status is
// deliberately kept separate from connection-health color).
func toggleMic(a *tray.App, w *watchdog) {
	port, err := ensurePort(w)
	if err != nil {
		log.Printf("ERROR: mic toggle failed: %v", err)
		return
	}

	client := sonarapi.NewClient(port)
	muted, err := client.ToggleMicMuted()
	if err != nil {
		log.Printf("ERROR: mic toggle failed: %v", err)
		return
	}

	w.mu.Lock()
	w.micMuted = muted
	w.mu.Unlock()

	status := "Mic: Live"
	if muted {
		status = "Mic: MUTED"
	}
	fmt.Printf("INFO: %s\n", status)
	a.SetTooltip(fmt.Sprintf("Sonar: Connected | Audio: OK | %s", status))
}

// unmuteBeforeWork sends one final unmute before Work Mode pauses
// monitoring — a safety net so a mute left on from gaming doesn't
// silently carry over into a work call.
func unmuteBeforeWork(a *tray.App, w *watchdog) {
	port, err := ensurePort(w)
	if err != nil {
		log.Printf("WARN: could not unmute before Work Mode: %v", err)
		return
	}

	client := sonarapi.NewClient(port)
	if err := client.SetMicMuted(false); err != nil {
		log.Printf("WARN: could not unmute before Work Mode: %v", err)
		return
	}

	w.mu.Lock()
	w.micMuted = false
	w.mu.Unlock()
	fmt.Println("INFO: Mic unmuted before entering Work Mode.")
}

// showAbout displays a native message box with app credit and the
// Ko-fi link. Uses MessageBoxW directly rather than pulling in a GUI
// framework for one static dialog.
func showAbout() {
	text := "SteelSeries Connection Assistant\n\n" +
		"Developed by Damon Downing, 2026\n\n" +
		"Support my development by contributing via Ko-fi. Any little bit helps!\n" +
		kofiURL + "\n\n" +
		"(Use \"Support on Ko-fi\" in the tray menu to open it directly.)"

	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		log.Printf("WARN: showAbout failed: %v", err)
		return
	}
	titlePtr, err := syscall.UTF16PtrFromString("About")
	if err != nil {
		log.Printf("WARN: showAbout failed: %v", err)
		return
	}

	procMessageBoxW := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), 0)
}

// openURL opens the given URL in the user's default browser.
func openURL(url string) {
	cmd := exec.Command("cmd", "/c", "start", "", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		log.Printf("WARN: failed to open %s: %v", url, err)
	}
}

func ensurePort(w *watchdog) (int, error) {
	w.mu.Lock()
	cached := w.port
	w.mu.Unlock()
	if cached != 0 {
		return cached, nil
	}

	port, err := sonarapi.DiscoverPort()
	if err != nil {
		return 0, err
	}

	w.mu.Lock()
	w.port = port
	w.mu.Unlock()
	return port, nil
}

// restartSonar kills SteelSeriesSonar.exe and waits for GG's own
// supervisor to relaunch it (confirmed by hand that this self-heals),
// then runs a check/fix once it's back. Blocks the watchdog loop for up
// to ~30s — deliberate, since this is a rare, explicitly user-triggered
// action, not something that should race with routine checks.
func restartSonar(a *tray.App, w *watchdog) {
	fmt.Println("INFO: Restarting SteelSeriesSonar.exe...")
	a.SetState(tray.StateProblem, "Restarting Sonar...")

	cmd := exec.Command("taskkill", "/F", "/IM", "SteelSeriesSonar.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		log.Printf("WARN: taskkill failed (maybe already stopped): %v", err)
	}

	w.mu.Lock()
	w.port = 0 // port will change after restart — force rediscovery
	w.mu.Unlock()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		if _, err := sonarapi.DiscoverPort(); err == nil {
			fmt.Println("INFO: Sonar is back.")
			runCheckAndFix(a, w)
			return
		}
	}

	log.Println("WARN: Sonar did not come back within 30s.")
	a.SetState(tray.StateProblem, "Sonar did not restart")
}
