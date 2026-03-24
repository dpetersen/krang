package tui

import (
	"testing"
)

func TestRepoPickerSetToggle(t *testing.T) {
	sets := map[string][]string{
		"backend": {"gonfalon", "gonfalon-priv"},
	}
	allRepos := []string{"catfood", "gonfalon", "gonfalon-priv"}

	p := newRepoPicker("test", sets, allRepos, Styles{})

	// First item should be the "backend" set.
	if p.items[0].Kind != pickerItemSet || p.items[0].Name != "backend" {
		t.Fatalf("expected set 'backend' at index 0, got %v", p.items[0])
	}

	// Toggle the set — should check the set and its member repo items.
	p.toggle()
	if !p.items[0].Checked {
		t.Error("set should be checked after toggle")
	}

	selected := p.selectedRepos()
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected repos, got %v", selected)
	}

	// catfood should be unchecked.
	for _, item := range p.items {
		if item.Name == "catfood" && item.Checked {
			t.Error("catfood should not be checked")
		}
	}

	// Now toggle catfood — set should remain checked.
	for i, item := range p.items {
		if item.Name == "catfood" {
			p.cursor = i
			break
		}
	}
	p.toggle()

	if !p.items[0].Checked {
		t.Error("backend set should still be checked after toggling catfood")
	}
	selected = p.selectedRepos()
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected repos, got %v", selected)
	}
}

func TestRepoPickerIndividualToggle(t *testing.T) {
	sets := map[string][]string{
		"backend": {"alpha", "beta"},
	}
	allRepos := []string{"alpha", "beta", "gamma"}

	p := newRepoPicker("test", sets, allRepos, Styles{})

	// Items: [set:backend, repo:alpha, repo:beta, repo:gamma]
	// Move to gamma and toggle it.
	for i, item := range p.items {
		if item.Name == "gamma" {
			p.cursor = i
			break
		}
	}
	p.toggle()

	selected := p.selectedRepos()
	if len(selected) != 1 || selected[0] != "gamma" {
		t.Fatalf("expected [gamma], got %v", selected)
	}
}

func TestRepoPickerSetSyncOnIndividual(t *testing.T) {
	sets := map[string][]string{
		"all": {"alpha", "beta"},
	}
	allRepos := []string{"alpha", "beta"}

	// Items: [set:all, repo:alpha, repo:beta]
	p := newRepoPicker("test", sets, allRepos, Styles{})

	if len(p.items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(p.items), p.items)
	}

	// Toggle the set — selects both member repo items.
	p.toggle()
	selected := p.selectedRepos()
	if len(selected) != 2 {
		t.Fatalf("expected 2 repos, got %v", selected)
	}

	// Untoggle alpha individually — set should become unchecked.
	p.cursor = 1 // alpha
	p.toggle()
	if p.items[0].Checked {
		t.Error("set should be unchecked when a member is unchecked")
	}

	// Re-check alpha — set should auto-check.
	p.toggle()
	if !p.items[0].Checked {
		t.Error("set should be checked when all members are checked")
	}
}

func TestRepoPickerNoSets(t *testing.T) {
	allRepos := []string{"alpha", "beta", "gamma"}

	p := newRepoPicker("test", nil, allRepos, Styles{})

	if len(p.items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(p.items))
	}

	// Toggle all.
	for i := range p.items {
		p.cursor = i
		p.toggle()
	}

	selected := p.selectedRepos()
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected, got %v", selected)
	}
}

func TestRepoPickerEmptySelection(t *testing.T) {
	p := newRepoPicker("test", nil, []string{"alpha"}, Styles{})
	selected := p.selectedRepos()
	if len(selected) != 0 {
		t.Fatalf("expected empty selection, got %v", selected)
	}
}
