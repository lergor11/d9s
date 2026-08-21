package ui

import "github.com/charmbracelet/lipgloss"

var (
	stHeader    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	stHeaderDim = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	stFooter    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	stSelected  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	stDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	stErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	stOK        = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	stWarn      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stBadge     = lipgloss.NewStyle().Foreground(lipgloss.Color("135"))
	stSection   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	stTableHead = lipgloss.NewStyle().Bold(true)
	stModal     = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")).
			Padding(1, 2)
	stHelpBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(1, 2)
	// stPopup frames the completion list, which sits over the results area
	// while the editor keeps the focus, so it stays as compact as possible.
	stPopup = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("135")).
		PaddingLeft(1).
		PaddingRight(1)
)
