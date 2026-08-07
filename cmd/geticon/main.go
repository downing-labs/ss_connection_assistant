// Command geticon is a one-off tool: generates app.ico (the compiled
// .exe's own icon — Explorer, taskbar, Alt+Tab) from the same composited
// headphone glyph the tray icon uses. Run once with `go run
// ./cmd/geticon` from the project root; re-run any time the glyph or
// colors change.
package main

import (
	"fmt"
	"os"

	"steelseries-connection-assistant/internal/tray"
)

func main() {
	data, err := tray.BuildAppIconICO()
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}

	if err := os.WriteFile("app.ico", data, 0o644); err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}

	fmt.Println("Wrote app.ico")
}
