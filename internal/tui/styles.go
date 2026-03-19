package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("244"))

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Background(lipgloss.Color("236"))

	attentionOKStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	attentionWaitingStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	attentionPermissionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	attentionErrorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	attentionDoneStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))

	stateActiveStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	stateParkedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	stateDormantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			MarginTop(1)

	inputLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	debugLogStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)
