package tui

import (
	"fmt"

	catppuccin "github.com/catppuccin/go"
	"github.com/charmbracelet/lipgloss"
)

type Theme struct {
	Title    lipgloss.Color
	Subtitle lipgloss.Color
	Border   lipgloss.Color
	Surface  lipgloss.Color
	Muted    lipgloss.Color
	Accent   lipgloss.Color

	OK      lipgloss.Color
	Active  lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color
	Done    lipgloss.Color
	Parked  lipgloss.Color
	Dormant lipgloss.Color
	Danger  lipgloss.Color
}

type Styles struct {
	theme Theme

	Title       lipgloss.Style
	Header      lipgloss.Style
	SelectedRow lipgloss.Style

	AttentionOK         lipgloss.Style
	AttentionWaiting    lipgloss.Style
	AttentionPermission lipgloss.Style
	AttentionError      lipgloss.Style
	AttentionDone       lipgloss.Style

	StateActive  lipgloss.Style
	StateParked  lipgloss.Style
	StateDormant lipgloss.Style

	StatusBar    lipgloss.Style
	InputLabel   lipgloss.Style
	ErrorText    lipgloss.Style
	WarningText  lipgloss.Style
	DebugLog     lipgloss.Style
	FlagSkull    lipgloss.Style
	ModalBorder  lipgloss.Color
	ModalTitle   lipgloss.Style
	ModalContent lipgloss.Style
}

func BuildStyles(theme Theme) Styles {
	return Styles{
		theme: theme,
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Title),
		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Subtitle),
		SelectedRow: lipgloss.NewStyle().
			Bold(true).
			Background(theme.Surface),

		AttentionOK:         lipgloss.NewStyle().Foreground(theme.OK),
		AttentionWaiting:    lipgloss.NewStyle().Foreground(theme.Warning),
		AttentionPermission: lipgloss.NewStyle().Bold(true).Foreground(theme.Error),
		AttentionError:      lipgloss.NewStyle().Foreground(theme.Error),
		AttentionDone:       lipgloss.NewStyle().Foreground(theme.Done),

		StateActive:  lipgloss.NewStyle().Foreground(theme.Active),
		StateParked:  lipgloss.NewStyle().Foreground(theme.Parked),
		StateDormant: lipgloss.NewStyle().Foreground(theme.Dormant),

		StatusBar: lipgloss.NewStyle().
			Foreground(theme.Subtitle).
			MarginTop(1),
		InputLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Title),
		ErrorText: lipgloss.NewStyle().
			Foreground(theme.Error),
		WarningText: lipgloss.NewStyle().
			Foreground(theme.Warning),
		DebugLog: lipgloss.NewStyle().
			Foreground(theme.Muted),
		FlagSkull: lipgloss.NewStyle().
			Foreground(theme.Danger),
		ModalBorder: theme.Border,
		ModalTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Title),
		ModalContent: lipgloss.NewStyle().
			Foreground(theme.Subtitle),
	}
}

var ClassicTheme = Theme{
	Title:    lipgloss.Color("205"),
	Subtitle: lipgloss.Color("244"),
	Border:   lipgloss.Color("244"),
	Surface:  lipgloss.Color("236"),
	Muted:    lipgloss.Color("240"),
	Accent:   lipgloss.Color("205"),

	OK:      lipgloss.Color("242"),
	Active:  lipgloss.Color("82"),
	Warning: lipgloss.Color("226"),
	Error:   lipgloss.Color("196"),
	Done:    lipgloss.Color("82"),
	Parked:  lipgloss.Color("110"),
	Dormant: lipgloss.Color("243"),
	Danger:  lipgloss.Color("208"),
}

func catppuccinTheme(flavor catppuccin.Flavor) Theme {
	return Theme{
		Title:    lipgloss.Color(flavor.Mauve().Hex),
		Subtitle: lipgloss.Color(flavor.Overlay1().Hex),
		Border:   lipgloss.Color(flavor.Surface2().Hex),
		Surface:  lipgloss.Color(flavor.Surface0().Hex),
		Muted:    lipgloss.Color(flavor.Overlay0().Hex),
		Accent:   lipgloss.Color(flavor.Pink().Hex),

		OK:      lipgloss.Color(flavor.Sapphire().Hex),
		Active:  lipgloss.Color(flavor.Green().Hex),
		Warning: lipgloss.Color(flavor.Yellow().Hex),
		Error:   lipgloss.Color(flavor.Red().Hex),
		Done:    lipgloss.Color(flavor.Green().Hex),
		Parked:  lipgloss.Color(flavor.Blue().Hex),
		Dormant: lipgloss.Color(flavor.Overlay0().Hex),
		Danger:  lipgloss.Color(flavor.Peach().Hex),
	}
}

var (
	CatppuccinLatteTheme     = catppuccinTheme(catppuccin.Latte)
	CatppuccinFrappeTheme    = catppuccinTheme(catppuccin.Frappe)
	CatppuccinMacchiatoTheme = catppuccinTheme(catppuccin.Macchiato)
	CatppuccinMochaTheme     = catppuccinTheme(catppuccin.Mocha)
)

const DefaultThemeName = "catppuccin-mocha"

var themeRegistry = map[string]Theme{
	"classic":               ClassicTheme,
	"catppuccin-latte":      CatppuccinLatteTheme,
	"catppuccin-frappe":     CatppuccinFrappeTheme,
	"catppuccin-macchiato":  CatppuccinMacchiatoTheme,
	"catppuccin-mocha":      CatppuccinMochaTheme,
}

func ResolveTheme(name string) (Theme, error) {
	if theme, ok := themeRegistry[name]; ok {
		return theme, nil
	}
	var names []string
	for n := range themeRegistry {
		names = append(names, n)
	}
	return Theme{}, fmt.Errorf("unknown theme %q; available: %v", name, names)
}

func RegisterTheme(name string, theme Theme) {
	themeRegistry[name] = theme
}

func ThemeNames() []string {
	var names []string
	for n := range themeRegistry {
		names = append(names, n)
	}
	return names
}
