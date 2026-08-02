package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stubVCSCommands swaps the VCS exec seam for one that answers from a
// canned responder, so tests can describe what a source repo already
// holds without standing one up.
func stubVCSCommands(t *testing.T, respond func(dir, name string, args ...string) (string, error)) *[]recordedCommand {
	t.Helper()

	original := runVCSCommand
	t.Cleanup(func() { runVCSCommand = original })

	var recorded []recordedCommand
	runVCSCommand = func(dir, name string, args ...string) (string, error) {
		recorded = append(recorded, recordedCommand{
			Dir:  dir,
			Args: append([]string{name}, args...),
		})
		return respond(dir, name, args...)
	}
	return &recorded
}

// noVCSNamesTaken answers every availability probe with "nothing here".
func noVCSNamesTaken(string, string, ...string) (string, error) { return "", nil }

func TestSlotIdentityDerivesNames(t *testing.T) {
	tests := []struct {
		name      string
		identity  SlotIdentity
		wantDir   string
		wantVCS   string
		wantBranc string
	}{
		{
			name:      "initial slot keeps the pre-slot names",
			identity:  SlotIdentity{TaskName: "slots", RepoName: "krang"},
			wantDir:   "krang",
			wantVCS:   "slots",
			wantBranc: "krang/slots",
		},
		{
			name:      "labeled slot names the task, repo, and label",
			identity:  SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "tests"},
			wantDir:   "krang--tests",
			wantVCS:   "slots--krang--tests",
			wantBranc: "krang/slots--krang--tests",
		},
		{
			name:      "auto-numbered slot reads as a label",
			identity:  SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "2"},
			wantDir:   "krang--2",
			wantVCS:   "slots--krang--2",
			wantBranc: "krang/slots--krang--2",
		},
		{
			name:      "a second repo in the same task gets its own names",
			identity:  SlotIdentity{TaskName: "slots", RepoName: "nojira", Label: "tests"},
			wantDir:   "nojira--tests",
			wantVCS:   "slots--nojira--tests",
			wantBranc: "krang/slots--nojira--tests",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.identity.DirName(); got != tc.wantDir {
				t.Errorf("DirName() = %q, want %q", got, tc.wantDir)
			}
			if got := tc.identity.VCSName(); got != tc.wantVCS {
				t.Errorf("VCSName() = %q, want %q", got, tc.wantVCS)
			}
			if got := tc.identity.GitBranch(); got != tc.wantBranc {
				t.Errorf("GitBranch() = %q, want %q", got, tc.wantBranc)
			}
		})
	}
}

func TestSlotIdentityNamesAreDeterministic(t *testing.T) {
	first := SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "tests"}
	second := SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "tests"}

	if first.DirName() != second.DirName() || first.VCSName() != second.VCSName() {
		t.Errorf("same identity produced different names: %+v vs %+v", first, second)
	}

	other := SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "docs"}
	if first.DirName() == other.DirName() || first.VCSName() == other.VCSName() {
		t.Errorf("two slots of one repo share names: %q / %q", first.DirName(), other.DirName())
	}
}

func TestValidateSlotLabel(t *testing.T) {
	valid := []string{"tests", "api-v2", "2", "a1-b2-c3"}
	for _, label := range valid {
		if err := ValidateSlotLabel(label); err != nil {
			t.Errorf("ValidateSlotLabel(%q) = %v, want nil", label, err)
		}
	}

	invalid := []string{"", "Tests", "with space", "-leading", "trailing-", "double--dash", "under_score", "slash/y"}
	for _, label := range invalid {
		if err := ValidateSlotLabel(label); err == nil {
			t.Errorf("ValidateSlotLabel(%q) = nil, want an error", label)
		}
	}
}

func TestSlotIdentityRejectsDirNameMatchingManagedRepo(t *testing.T) {
	rs, reposDir, _ := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	// Somebody also manages a repo literally named krang--tests, so the
	// obvious slot directory for "krang" labeled "tests" is taken.
	markRepoDir(t, filepath.Join(reposDir, "krang--tests"), "jj")

	identity := SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "tests"}
	err := identity.Validate(rs)
	if err == nil {
		t.Fatal("Validate accepted a slot directory that shadows a managed repo")
	}
	if !strings.Contains(err.Error(), "krang--tests") {
		t.Errorf("error %q should name the colliding directory", err)
	}

	// The initial slot of a repo is named after that repo by design.
	if err := (SlotIdentity{TaskName: "slots", RepoName: "krang"}).Validate(rs); err != nil {
		t.Errorf("Validate rejected an initial slot: %v", err)
	}
}

func TestResolveSlotIdentityAutoNumbers(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)
	stubVCSCommands(t, noVCSNamesTaken)

	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	makeDir(t, workspaceDir)

	// Nothing there yet: the repo takes its initial slot.
	identity, err := ResolveSlotIdentity(rs, workspaceDir, "slots", "krang", "")
	if err != nil {
		t.Fatalf("ResolveSlotIdentity: %v", err)
	}
	if identity.Label != "" || identity.DirName() != "krang" || identity.VCSName() != "slots" {
		t.Fatalf("first slot = %+v, want the pre-slot names", identity)
	}

	// With the initial slot taken, the next one auto-numbers.
	markRepoDir(t, filepath.Join(workspaceDir, "krang"), "jj")
	identity, err = ResolveSlotIdentity(rs, workspaceDir, "slots", "krang", "")
	if err != nil {
		t.Fatalf("ResolveSlotIdentity: %v", err)
	}
	if identity.Label != "2" || identity.DirName() != "krang--2" || identity.VCSName() != "slots--krang--2" {
		t.Fatalf("second slot = %+v, want the 2 discriminator", identity)
	}

	// And keeps counting past whatever is already there.
	markRepoDir(t, filepath.Join(workspaceDir, "krang--2"), "jj")
	identity, err = ResolveSlotIdentity(rs, workspaceDir, "slots", "krang", "")
	if err != nil {
		t.Fatalf("ResolveSlotIdentity: %v", err)
	}
	if identity.Label != "3" {
		t.Fatalf("third slot = %+v, want the 3 discriminator", identity)
	}
}

func TestResolveSlotIdentityRefusesTakenJJWorkspaceName(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)
	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	makeDir(t, workspaceDir)

	// The source repo already has a workspace under the name this slot
	// would want — another task claimed the same label.
	stubVCSCommands(t, func(_, name string, args ...string) (string, error) {
		if name == "jj" && len(args) > 1 && args[0] == "workspace" && args[1] == "list" {
			return "default: qpvuntsm 4c1e (empty)\nslots--krang--tests: zzzzzzzz 8a2b (empty)\n", nil
		}
		return "", nil
	})

	_, err := ResolveSlotIdentity(rs, workspaceDir, "slots", "krang", "tests")
	if err == nil {
		t.Fatal("ResolveSlotIdentity accepted a jj workspace name already in use")
	}
	if !strings.Contains(err.Error(), "slots--krang--tests") {
		t.Errorf("error %q should name the collision", err)
	}
}

func TestCloneRepoAsRefusesTakenJJWorkspaceName(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)
	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	makeDir(t, workspaceDir)

	commands := stubVCSCommands(t, func(_, name string, args ...string) (string, error) {
		if name == "jj" && len(args) > 1 && args[0] == "workspace" && args[1] == "list" {
			return "slots--krang--tests: zzzzzzzz 8a2b (empty)\n", nil
		}
		return "", nil
	})

	identity := SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "tests"}
	result := CloneRepoAs(rs, identity, SlotDst(rs, workspaceDir, identity))
	if result.Err == nil {
		t.Fatal("CloneRepoAs created a working copy over an existing jj workspace name")
	}
	if !strings.Contains(result.Err.Error(), "slots--krang--tests") {
		t.Errorf("error %q should name the collision", result.Err)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "krang--tests")); !os.IsNotExist(err) {
		t.Error("refused slot should not have left a directory behind")
	}

	want := []recordedCommand{{
		Dir:  filepath.Join(reposDir, "krang"),
		Args: []string{"jj", "workspace", "list"},
	}}
	if !reflect.DeepEqual(*commands, want) {
		t.Errorf("commands = %v, want only the availability probe", *commands)
	}
}

func TestCloneRepoAsRefusesTakenGitBranch(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)
	markRepoDir(t, filepath.Join(reposDir, "krang"), "git")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	makeDir(t, workspaceDir)

	stubVCSCommands(t, func(_, name string, args ...string) (string, error) {
		if name == "git" && len(args) > 0 && args[0] == "branch" {
			return "  krang/slots--krang--tests\n", nil
		}
		return "", nil
	})

	identity := SlotIdentity{TaskName: "slots", RepoName: "krang", Label: "tests"}
	result := CloneRepoAs(rs, identity, SlotDst(rs, workspaceDir, identity))
	if result.Err == nil {
		t.Fatal("CloneRepoAs created a worktree on a branch that already exists")
	}
	if !strings.Contains(result.Err.Error(), "krang/slots--krang--tests") {
		t.Errorf("error %q should name the colliding branch", result.Err)
	}
}

func TestParseSlotDirName(t *testing.T) {
	rs, reposDir, _ := multiRepoSets(t)
	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	markRepoDir(t, filepath.Join(reposDir, "krang--fork"), "jj")

	tests := []struct {
		dirName   string
		wantRepo  string
		wantLabel string
	}{
		{"krang", "krang", ""},
		{"krang--tests", "krang", "tests"},
		{"krang--2", "krang", "2"},
		// A managed repo whose own name holds the separator wins over
		// reading it as a slot of the shorter repo.
		{"krang--fork", "krang--fork", ""},
		{"krang--fork--tests", "krang--fork", "tests"},
		// Nothing in the registry matches: fall back to the convention.
		{"oneoff", "oneoff", ""},
		{"oneoff--scratch", "oneoff", "scratch"},
	}

	for _, tc := range tests {
		repo, label := ParseSlotDirName(rs, tc.dirName)
		if repo != tc.wantRepo || label != tc.wantLabel {
			t.Errorf("ParseSlotDirName(%q) = (%q, %q), want (%q, %q)",
				tc.dirName, repo, label, tc.wantRepo, tc.wantLabel)
		}
	}
}

func TestPresentReposSeesSlotDirsAsTheirRepo(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	markRepoDir(t, filepath.Join(reposDir, "nojira"), "git")
	markRepoDir(t, filepath.Join(reposDir, "untouched"), "git")

	workspaceDir := filepath.Join(workspacesDir, "slots")
	markRepoDir(t, filepath.Join(workspaceDir, "krang"), "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "krang--tests"), "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "nojira"), "git")
	mkdirs(t, workspaceDir, "notes")

	recorded := []RepoProvenance{
		{DirName: "krang", RepoName: "krang", VCS: "jj", VCSName: "slots"},
		{DirName: "krang--tests", RepoName: "krang", VCS: "jj", VCSName: "slots--krang--tests", Label: "tests"},
	}

	// The picker hides repos that are present. A second slot of krang
	// must not read as a repo of its own, and nojira must not vanish.
	got := PresentRepos(rs, workspaceDir, recorded)
	want := []string{"krang", "nojira"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PresentRepos = %v, want %v", got, want)
	}

	// The same holds with no rows at all, from the directory names.
	if got := PresentRepos(rs, workspaceDir, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("PresentRepos without rows = %v, want %v", got, want)
	}
}

func TestPresentSlotsResolvesUnrecordedSlotDirs(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	markRepoDir(t, filepath.Join(workspaceDir, "krang"), "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "krang--tests"), "jj")

	slots := PresentSlots(rs, workspaceDir, nil)
	want := []RepoProvenance{
		{DirName: "krang", RepoName: "krang", VCS: "jj", VCSName: "slots"},
		{DirName: "krang--tests", RepoName: "krang", VCS: "jj", VCSName: "slots--krang--tests", Label: "tests"},
	}
	if !reflect.DeepEqual(slots, want) {
		t.Errorf("PresentSlots = %+v, want %+v", slots, want)
	}
}

func TestCloneRepoKeepsInitialSlotNames(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	makeDir(t, reposDir)
	initGitRepo(t, filepath.Join(reposDir, "alpha"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	workspaceDir := filepath.Join(workspacesDir, "legacy-names")
	makeDir(t, workspaceDir)

	result := CloneRepo(rs, "legacy-names", filepath.Join(workspaceDir, "alpha"), "alpha")
	if result.Err != nil {
		t.Fatalf("CloneRepo: %v", result.Err)
	}

	want := RepoProvenance{
		DirName:  "alpha",
		RepoName: "alpha",
		VCS:      "git",
		VCSName:  "legacy-names",
	}
	if result.Provenance != want {
		t.Errorf("provenance = %+v, want %+v", result.Provenance, want)
	}

	branches := gitBranchList(t, filepath.Join(reposDir, "alpha"))
	if !strings.Contains(branches, "krang/legacy-names") {
		t.Errorf("branches = %q, want the pre-slot krang/legacy-names", branches)
	}
}

func TestCloneRepoAsGivesTwoSlotsOfOneRepoDistinctBranches(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	makeDir(t, reposDir)
	initGitRepo(t, filepath.Join(reposDir, "alpha"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	workspaceDir := filepath.Join(workspacesDir, "slots")
	makeDir(t, workspaceDir)

	for _, label := range []string{"", "tests"} {
		identity, err := ResolveSlotIdentity(rs, workspaceDir, "slots", "alpha", label)
		if err != nil {
			t.Fatalf("ResolveSlotIdentity(%q): %v", label, err)
		}
		result := CloneRepoAs(rs, identity, SlotDst(rs, workspaceDir, identity))
		if result.Err != nil {
			t.Fatalf("CloneRepoAs(%q): %v", label, result.Err)
		}
	}

	for _, dirName := range []string{"alpha", "alpha--tests"} {
		if _, err := os.Stat(filepath.Join(workspaceDir, dirName, ".git")); err != nil {
			t.Errorf("expected a working copy at %s: %v", dirName, err)
		}
	}

	branches := gitBranchList(t, filepath.Join(reposDir, "alpha"))
	for _, branch := range []string{"krang/slots", "krang/slots--alpha--tests"} {
		if !strings.Contains(branches, branch) {
			t.Errorf("branches = %q, want %s", branches, branch)
		}
	}

	// Asking for the same label again must refuse rather than reuse.
	if _, err := ResolveSlotIdentity(rs, workspaceDir, "slots", "alpha", "tests"); err == nil {
		t.Error("ResolveSlotIdentity handed out a slot label that is already in use")
	}
}

func TestCloneRepoRefusesBranchHoldingUnmergedWork(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	makeDir(t, reposDir)
	repoDir := filepath.Join(reposDir, "alpha")
	initGitRepo(t, repoDir)

	// A previous task of the same name left a branch behind that
	// cleanup deliberately kept, because it holds unpushed commits.
	run(t, repoDir, "git", "commit", "--allow-empty", "-m", "unpushed work")
	run(t, repoDir, "git", "branch", "krang/reused-name")
	run(t, repoDir, "git", "reset", "--hard", "HEAD~1")

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	workspaceDir := filepath.Join(workspacesDir, "reused-name")
	makeDir(t, workspaceDir)

	result := CloneRepo(rs, "reused-name", filepath.Join(workspaceDir, "alpha"), "alpha")
	if result.Err == nil {
		t.Fatal("CloneRepo reused a branch holding unmerged work")
	}
	if !strings.Contains(result.Err.Error(), "krang/reused-name") {
		t.Errorf("error %q should name the branch it refused", result.Err)
	}

	branches := gitBranchList(t, repoDir)
	if !strings.Contains(branches, "krang/reused-name") {
		t.Errorf("branches = %q, want the unmerged branch left alone", branches)
	}
}

func makeDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func gitBranchList(t *testing.T, repoDir string) string {
	t.Helper()
	output, err := runVCSCommand(repoDir, "git", "branch", "--list")
	if err != nil {
		t.Fatalf("git branch --list: %v: %s", err, output)
	}
	return output
}
