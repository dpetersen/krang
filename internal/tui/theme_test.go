package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestBuildStylesClassicTheme(t *testing.T) {
	styles := BuildStyles(ClassicTheme)

	tests := []struct {
		name  string
		style lipgloss.Style
		fg    lipgloss.Color
	}{
		{"Title", styles.Title, ClassicTheme.Title},
		{"Header", styles.Header, ClassicTheme.Subtitle},
		{"AttentionOK", styles.AttentionOK, ClassicTheme.OK},
		{"AttentionWaiting", styles.AttentionWaiting, ClassicTheme.Warning},
		{"AttentionPermission", styles.AttentionPermission, ClassicTheme.Error},
		{"AttentionError", styles.AttentionError, ClassicTheme.Error},
		{"AttentionDone", styles.AttentionDone, ClassicTheme.Done},
		{"StateActive", styles.StateActive, ClassicTheme.Active},
		{"StateParked", styles.StateParked, ClassicTheme.Parked},
		{"StateDormant", styles.StateDormant, ClassicTheme.Dormant},
		{"InputLabel", styles.InputLabel, ClassicTheme.Title},
		{"ErrorText", styles.ErrorText, ClassicTheme.Error},
		{"WarningText", styles.WarningText, ClassicTheme.Warning},
		{"DebugLog", styles.DebugLog, ClassicTheme.Muted},
		{"FlagSkull", styles.FlagSkull, ClassicTheme.Danger},
		{"StatusBar", styles.StatusBar, ClassicTheme.Subtitle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.style.GetForeground()
			if got != tt.fg {
				t.Errorf("foreground = %v, want %v", got, tt.fg)
			}
		})
	}
}

func TestBuildStylesSelectedRowBackground(t *testing.T) {
	styles := BuildStyles(ClassicTheme)
	got := styles.SelectedRow.GetBackground()
	if got != ClassicTheme.Surface {
		t.Errorf("SelectedRow background = %v, want %v", got, ClassicTheme.Surface)
	}
}

func TestBuildStylesBoldStyles(t *testing.T) {
	styles := BuildStyles(ClassicTheme)

	boldStyles := []struct {
		name string
		style lipgloss.Style
	}{
		{"Title", styles.Title},
		{"Header", styles.Header},
		{"SelectedRow", styles.SelectedRow},
		{"AttentionPermission", styles.AttentionPermission},
		{"InputLabel", styles.InputLabel},
	}

	for _, tt := range boldStyles {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.style.GetBold() {
				t.Errorf("expected bold")
			}
		})
	}
}

func TestResolveThemeValid(t *testing.T) {
	theme, err := ResolveTheme("classic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if theme.Title != ClassicTheme.Title {
		t.Errorf("got %v, want %v", theme.Title, ClassicTheme.Title)
	}
}

func TestResolveThemeInvalid(t *testing.T) {
	_, err := ResolveTheme("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown theme")
	}
}

func TestCatppuccinThemesRegistered(t *testing.T) {
	flavors := []string{
		"catppuccin-latte",
		"catppuccin-frappe",
		"catppuccin-macchiato",
		"catppuccin-mocha",
	}
	for _, name := range flavors {
		t.Run(name, func(t *testing.T) {
			theme, err := ResolveTheme(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Every color role should be non-empty (catppuccin hex values).
			if theme.Title == "" {
				t.Error("Title color is empty")
			}
			if theme.Error == "" {
				t.Error("Error color is empty")
			}
			// BuildStyles should not panic.
			styles := BuildStyles(theme)
			if styles.Title.GetForeground() != theme.Title {
				t.Errorf("Title foreground mismatch")
			}
		})
	}
}

func TestDefaultThemeResolves(t *testing.T) {
	_, err := ResolveTheme(DefaultThemeName)
	if err != nil {
		t.Fatalf("default theme %q failed to resolve: %v", DefaultThemeName, err)
	}
}

func TestClassicThemeMatchesOriginalColors(t *testing.T) {
	// Verify the classic theme preserves the exact ANSI 256 color
	// numbers from the original hardcoded styles.
	tests := []struct {
		role     string
		got      lipgloss.Color
		wantCode string
	}{
		{"Title", ClassicTheme.Title, "205"},
		{"Subtitle", ClassicTheme.Subtitle, "244"},
		{"Surface", ClassicTheme.Surface, "236"},
		{"OK", ClassicTheme.OK, "242"},
		{"Active", ClassicTheme.Active, "82"},
		{"Warning", ClassicTheme.Warning, "226"},
		{"Error", ClassicTheme.Error, "196"},
		{"Done", ClassicTheme.Done, "82"},
		{"Parked", ClassicTheme.Parked, "110"},
		{"Dormant", ClassicTheme.Dormant, "243"},
		{"Muted", ClassicTheme.Muted, "240"},
		{"Danger", ClassicTheme.Danger, "208"},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if string(tt.got) != tt.wantCode {
				t.Errorf("color = %q, want %q", string(tt.got), tt.wantCode)
			}
		})
	}
}
