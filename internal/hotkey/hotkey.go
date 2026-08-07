//go:build windows

// Package hotkey registers a single global hotkey (Pause/Break, no
// modifiers) using Windows' RegisterHotKey API — a targeted, well-
// documented mechanism for one specific key combo, deliberately chosen
// over a system-wide low-level keyboard hook (which watches every
// keystroke and is the same mechanism keyloggers use; there's no reason
// to reach for that when we only care about one key).
//
// Uses PeekMessage in a polling loop (checked every 50ms) rather than
// the more typical blocking GetMessage, purely so Stop() can cleanly
// signal shutdown without needing PostThreadMessage plumbing. The
// 50ms latency on key presses is imperceptible for a mute toggle.
package hotkey

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	modUser32             = syscall.NewLazyDLL("user32.dll")
	procRegisterHotKey    = modUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey  = modUser32.NewProc("UnregisterHotKey")
	procPeekMessageW      = modUser32.NewProc("PeekMessageW")
)

const (
	// vkPause is the virtual-key code for Pause/Break.
	vkPause = 0x13
	// wmHotkey is the message posted when a registered hotkey fires.
	wmHotkey = 0x0312
	// pmRemove tells PeekMessage to remove the message from the queue
	// once read (vs. just peeking and leaving it there).
	pmRemove = 0x0001
	// hotkeyID is an arbitrary ID for our single registered hotkey.
	hotkeyID = 1
)

type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// Listener owns one registered global hotkey and its dedicated
// message-loop thread. Create with Start, read from Pressed for each
// key press, and call Stop when done (e.g. switching to Work Mode).
type Listener struct {
	Pressed chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

// Start registers the Pause/Break hotkey and begins listening. Returns
// an error if registration fails (e.g. another app already claimed
// Pause/Break as a global hotkey).
func Start() (*Listener, error) {
	l := &Listener{
		Pressed: make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	ready := make(chan error, 1)
	go l.run(ready)

	if err := <-ready; err != nil {
		return nil, err
	}
	return l, nil
}

// Stop unregisters the hotkey and stops the listener goroutine. Blocks
// until fully shut down.
func (l *Listener) Stop() {
	close(l.stop)
	<-l.done
}

func (l *Listener) run(ready chan<- error) {
	// RegisterHotKey/UnregisterHotKey and the message queue they use
	// are all tied to the calling thread — must stay on the same OS
	// thread for the lifetime of this listener.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r, _, callErr := procRegisterHotKey.Call(0, hotkeyID, 0, vkPause)
	if r == 0 {
		ready <- fmt.Errorf("RegisterHotKey (Pause/Break) failed: %v", callErr)
		close(l.done)
		return
	}
	defer procUnregisterHotKey.Call(0, hotkeyID)

	ready <- nil

	for {
		select {
		case <-l.stop:
			close(l.done)
			return
		default:
		}

		var m winMsg
		got, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if got != 0 {
			if m.Message == wmHotkey {
				select {
				case l.Pressed <- struct{}{}:
				default:
					// A press is already pending and hasn't been
					// consumed yet — drop this one rather than block.
				}
			}
			continue // check for more queued messages before sleeping
		}

		time.Sleep(50 * time.Millisecond)
	}
}
