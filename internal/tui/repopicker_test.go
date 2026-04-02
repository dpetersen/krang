package tui

import (
	"testing"

	"github.com/dpetersen/krang/internal/workspace"
)

func TestRepoPickerSetToggle(t *testing.T) {
	sets := map[string][]string{
		"backend": {"api-server", "web-app"},
	}
	allRepos := []string{"payments", "api-server", "web-app"}

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

	// payments should be unchecked.
	for _, item := range p.items {
		if item.Name == "payments" && item.Checked {
			t.Error("payments should not be checked")
		}
	}

	// Now toggle payments — set should remain checked.
	for i, item := range p.items {
		if item.Name == "payments" {
			p.cursor = i
			break
		}
	}
	p.toggle()

	if !p.items[0].Checked {
		t.Error("backend set should still be checked after toggling payments")
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

func TestTabbedPickerDefaultsToLocal(t *testing.T) {
	tp := newTabbedRepoPicker("test", nil, []string{"alpha"}, Styles{}, nil, true)
	if tp.activeTab != pickerTabLocal {
		t.Error("expected default tab to be Local")
	}
}

func TestTabbedPickerTabSwitching(t *testing.T) {
	tp := newTabbedRepoPicker("test", nil, []string{"alpha"}, Styles{}, nil, true)

	tp.switchToRemote()
	if tp.activeTab != pickerTabRemote {
		t.Error("expected Remote tab after switchToRemote")
	}

	tp.switchToLocal()
	if tp.activeTab != pickerTabLocal {
		t.Error("expected Local tab after switchToLocal")
	}
}

func TestTabbedPickerSelectedReposDelegates(t *testing.T) {
	tp := newTabbedRepoPicker("test", nil, []string{"alpha", "beta"}, Styles{}, nil, true)

	// Toggle first repo in local picker.
	tp.local.toggle()

	selected := tp.selectedRepos()
	if len(selected) != 1 || selected[0] != "alpha" {
		t.Fatalf("expected [alpha], got %v", selected)
	}
}

func TestTabbedPickerRemoteOrgEntry(t *testing.T) {
	tp := newTabbedRepoPicker("test", nil, []string{}, Styles{}, nil, true)
	tp.switchToRemote()

	if tp.remote.phase != remotePhaseOrgEntry {
		t.Error("expected org entry phase with no config orgs")
	}
}

func TestTabbedPickerRemoteOrgSelectWithConfigOrgs(t *testing.T) {
	rs := &workspace.RepoSets{GitHubOrgs: []string{"myorg", "other"}}
	tp := newTabbedRepoPicker("test", nil, []string{}, Styles{}, rs, true)
	tp.switchToRemote()

	if tp.remote.phase != remotePhaseOrgSelect {
		t.Error("expected org select phase with config orgs")
	}

	options := tp.orgSelectOptions()
	if len(options) != 3 {
		t.Fatalf("expected 3 options (2 orgs + Other...), got %d", len(options))
	}
	if options[0] != "myorg" || options[1] != "other" || options[2] != "Other..." {
		t.Errorf("unexpected options: %v", options)
	}
}

func TestTabbedPickerRemoteSelectedRepo(t *testing.T) {
	tp := newTabbedRepoPicker("test", nil, []string{}, Styles{}, nil, true)
	tp.remote.results = []string{"repo-a", "repo-b", "repo-c"}
	tp.remote.cursor = 1

	got := tp.remoteSelectedRepo()
	if got != "repo-b" {
		t.Errorf("remoteSelectedRepo = %q, want %q", got, "repo-b")
	}
}

func TestTabbedPickerGhUnavailable(t *testing.T) {
	tp := newTabbedRepoPicker("test", nil, []string{}, Styles{}, nil, false)
	tp.switchToRemote()

	if tp.remote.ghAvailable {
		t.Error("expected ghAvailable=false")
	}

	body := tp.renderRemoteBody()
	if !contains(body, "GitHub CLI") {
		t.Error("expected gh unavailable message in remote body")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
