package tray

import "github.com/getlantern/systray"

// Mode is the user-selected operating mode.
type Mode int

const (
	// ModeGame actively monitors Sonar and fixes audio routing drift.
	ModeGame Mode = iota
	// ModeWork pauses monitoring entirely — no reads, no fixes.
	ModeWork
)

// App wraps the system tray icon, tooltip, and right-click menu.
type App struct {
	// ResetCh receives a value when "Reset" is clicked — re-check and
	// fix audio routing immediately.
	ResetCh chan struct{}
	// RestartSonarCh receives a value when "Restart Sonar" is clicked.
	RestartSonarCh chan struct{}
	// AboutCh receives a value when "About" is clicked.
	AboutCh chan struct{}
	// KofiCh receives a value when "Support on Ko-fi" is clicked.
	KofiCh chan struct{}
	// ModeCh receives the new Mode whenever the user switches via the
	// Game Mode / Work Mode menu checkboxes.
	ModeCh chan Mode

	gameModeItem *systray.MenuItem
	workModeItem *systray.MenuItem
	resetItem    *systray.MenuItem
	restartItem  *systray.MenuItem
	aboutItem    *systray.MenuItem
	kofiItem     *systray.MenuItem
	exitItem     *systray.MenuItem
}

// New creates a tray App. Call Run to actually start it.
func New() *App {
	return &App{
		ResetCh:        make(chan struct{}, 1),
		RestartSonarCh: make(chan struct{}, 1),
		AboutCh:        make(chan struct{}, 1),
		KofiCh:         make(chan struct{}, 1),
		ModeCh:         make(chan Mode, 1),
	}
}

// Run starts the tray icon and blocks until Exit is clicked. onReady is
// called once the tray icon and menu exist, so it can start background
// work (the watchdog loop).
func (a *App) Run(onReady func(a *App)) {
	systray.Run(func() {
		systray.SetIcon(LoadIconBytes(StateProblem))
		systray.SetTitle("")
		systray.SetTooltip("SteelSeries Connection Assistant — starting...")

		a.gameModeItem = systray.AddMenuItemCheckbox("Game Mode", "Actively monitor and fix audio routing", true)
		a.workModeItem = systray.AddMenuItemCheckbox("Work Mode", "Pause monitoring — leave audio routing alone", false)
		systray.AddSeparator()
		a.resetItem = systray.AddMenuItem("Reset", "Re-check and fix audio routing now")
		a.restartItem = systray.AddMenuItem("Restart Sonar", "Kill and relaunch SteelSeriesSonar.exe (brief audio interruption)")
		systray.AddSeparator()
		a.aboutItem = systray.AddMenuItem("About", "About this app")
		a.kofiItem = systray.AddMenuItem("Support on Ko-fi", "Open ko-fi.com/hackpig1974")
		a.exitItem = systray.AddMenuItem("Exit", "Quit")

		go a.handleMenuClicks()

		if onReady != nil {
			onReady(a)
		}
	}, func() {
		// onExit — nothing to clean up here.
	})
}

func (a *App) handleMenuClicks() {
	for {
		select {
		case <-a.gameModeItem.ClickedCh:
			a.setMode(ModeGame)
		case <-a.workModeItem.ClickedCh:
			a.setMode(ModeWork)
		case <-a.resetItem.ClickedCh:
			select {
			case a.ResetCh <- struct{}{}:
			default:
			}
		case <-a.restartItem.ClickedCh:
			select {
			case a.RestartSonarCh <- struct{}{}:
			default:
			}
		case <-a.aboutItem.ClickedCh:
			select {
			case a.AboutCh <- struct{}{}:
			default:
			}
		case <-a.kofiItem.ClickedCh:
			select {
			case a.KofiCh <- struct{}{}:
			default:
			}
		case <-a.exitItem.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (a *App) setMode(m Mode) {
	if m == ModeGame {
		a.gameModeItem.Check()
		a.workModeItem.Uncheck()
	} else {
		a.workModeItem.Check()
		a.gameModeItem.Uncheck()
	}
	select {
	case a.ModeCh <- m:
	default:
	}
}

// SetState updates the tray icon color and tooltip.
func (a *App) SetState(state State, tooltip string) {
	systray.SetIcon(LoadIconBytes(state))
	systray.SetTooltip(tooltip)
}

// SetTooltip updates only the tooltip text, leaving the icon color
// untouched. Used for mic-mute status, which is deliberately kept
// separate from the connection-health icon color.
func (a *App) SetTooltip(tooltip string) {
	systray.SetTooltip(tooltip)
}
