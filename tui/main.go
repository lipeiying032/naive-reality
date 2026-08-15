package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.0"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("naivereal-tui", version)
		return
	}
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
