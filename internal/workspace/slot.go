package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// slotSeparator joins the parts of a slot's directory and VCS names.
// Labels may not contain it, so a name built from it can always be
// split back into its repo and label at the last occurrence.
const slotSeparator = "--"

// maxSlotDiscriminator bounds the auto-numbering search. A task holding
// a hundred working copies of one repo is a mistake, not a workflow.
const maxSlotDiscriminator = 100

// MaxSlotsPerTask caps how many working copies one task's workspace may
// hold. Each slot is a full checkout on disk and one more thing for the
// agent working in that workspace to confuse with its neighbours, so
// sprawl is refused rather than merely discouraged. The cap is enforced
// on the workspace HTTP API, which is the path an agent adds slots
// through; the human driving the TUI is trusted with their own repo
// picker.
const MaxSlotsPerTask = 4

// slotLabelPattern allows lowercase alphanumerics separated by single
// dashes. Uppercase would collide on case-insensitive filesystems,
// leading/trailing dashes read as typos, and a doubled dash would make
// <repo>--<label> ambiguous to split apart again.
var slotLabelPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateSlotLabel reports whether a user-supplied slot label is
// usable as part of a directory name, a jj workspace name, and a git
// branch name.
func ValidateSlotLabel(label string) error {
	if label == "" {
		return fmt.Errorf("slot label is empty")
	}
	if !slotLabelPattern.MatchString(label) {
		return fmt.Errorf("invalid slot label %q: use lowercase letters and digits separated by single dashes", label)
	}
	return nil
}

// SlotIdentity names one working copy inside a task's workspace: which
// repo it holds, and which slot of that repo it is. Every name krang
// gives that working copy — its directory, its jj workspace, its git
// branch — is derived from here, so the same slot always resolves to
// the same names.
//
// The empty label marks a task's initial working copy of a repo. It
// keeps the names krang used before slots existed (directory <repo>,
// VCS identity <task>) so nothing already on disk has to be renamed.
type SlotIdentity struct {
	TaskName string
	RepoName string
	Label    string

	// Base is the revset (jj) or commit-ish (git) the working copy
	// starts from. Empty means "detect the remote default branch",
	// which is what every krang-created slot did before callers could
	// ask for something else. It takes no part in any derived name, so
	// it plays no role in resolving or validating an identity — it
	// rides here so that the one path that creates working copies is
	// also the one path that knows, and can report back, where a
	// working copy started.
	Base string
}

// DirName returns the slot's directory name inside the workspace dir.
func (s SlotIdentity) DirName() string {
	if s.Label == "" {
		return s.RepoName
	}
	return s.RepoName + slotSeparator + s.Label
}

// VCSName returns the jj workspace name the slot owns. It includes the
// repo as well as the label because jj workspace names are unique per
// source repo, not per task, and two tasks may label slots the same way.
func (s SlotIdentity) VCSName() string {
	if s.Label == "" {
		return s.TaskName
	}
	return s.TaskName + slotSeparator + s.RepoName + slotSeparator + s.Label
}

// gitBranchPrefix namespaces every branch krang creates. Creation,
// cleanup, and the warnings the completion modal renders all have to
// agree on it, so it lives in one place.
const gitBranchPrefix = "krang/"

// GitBranch returns the branch a git slot is checked out on.
func (s SlotIdentity) GitBranch() string {
	return gitBranchPrefix + s.VCSName()
}

// Validate rejects identities that can't be given names safely. Passing
// a nil RepoSets skips the check that a slot directory would not shadow
// a managed repo.
func (s SlotIdentity) Validate(rs *RepoSets) error {
	if s.TaskName == "" {
		return fmt.Errorf("slot identity has no task name")
	}
	if s.RepoName == "" {
		return fmt.Errorf("slot identity has no repo name")
	}
	if s.Label == "" {
		// A task's initial working copy is named after the repo by
		// design, so the shadowing check below would always fire.
		return nil
	}
	if err := ValidateSlotLabel(s.Label); err != nil {
		return err
	}
	if rs == nil {
		return nil
	}

	// A slot directory that spells a managed repo's name makes the
	// workspace unreadable: the directory-name fallback would resolve
	// it to the wrong repo, and a later slot of that repo would want
	// the same directory.
	repos, err := rs.ListRepos()
	if err != nil {
		return nil
	}
	for _, repo := range repos {
		if repo == s.DirName() {
			return fmt.Errorf(
				"slot directory %q would shadow the managed repo of the same name: choose a different label",
				s.DirName())
		}
	}
	return nil
}

// SlotDst returns the path a slot's working copy lives at. In
// single_repo mode the workspace directory itself is the task's initial
// working copy; every other slot is a subdirectory.
func SlotDst(rs *RepoSets, workspaceDir string, identity SlotIdentity) string {
	return SlotPath(rs, workspaceDir, identity.DirName(), identity.Label)
}

// SlotPath is SlotDst for a caller holding a recorded row rather than an
// identity: the directory name and label are what the answer depends on,
// and a row has both without needing the task name back.
func SlotPath(rs *RepoSets, workspaceDir, dirName, label string) string {
	if rs != nil && rs.WorkspaceStrategy == StrategySingleRepo && label == "" {
		return workspaceDir
	}
	return filepath.Join(workspaceDir, dirName)
}

// ParseSlotDirName resolves a workspace subdirectory back to the repo
// it holds and the label it was created with. Managed repo names win
// outright, so a repo whose own name contains the separator is never
// mistaken for a slot; otherwise the longest managed repo the directory
// is prefixed with claims it. Directories matching nothing in the
// registry are split at the last separator, since labels can't contain
// one.
func ParseSlotDirName(rs *RepoSets, dirName string) (repo, label string) {
	if rs != nil {
		if repos, err := rs.ListRepos(); err == nil {
			longestPrefix := ""
			for _, candidate := range repos {
				if candidate == dirName {
					return dirName, ""
				}
				if strings.HasPrefix(dirName, candidate+slotSeparator) && len(candidate) > len(longestPrefix) {
					longestPrefix = candidate
				}
			}
			if longestPrefix != "" {
				return longestPrefix, strings.TrimPrefix(dirName, longestPrefix+slotSeparator)
			}
		}
	}

	if idx := strings.LastIndex(dirName, slotSeparator); idx > 0 {
		return dirName[:idx], dirName[idx+len(slotSeparator):]
	}
	return dirName, ""
}

// PresentSlots describes every working copy in a task's workspace
// directory, resolved back to the repo it holds. Recorded provenance is
// authoritative; directories with no row fall back to the slot naming
// convention, which is all krang can know about a checkout somebody
// made by hand.
func PresentSlots(rs *RepoSets, workspaceDir string, recorded []RepoProvenance) []RepoProvenance {
	byDir := make(map[string]RepoProvenance, len(recorded))
	for _, row := range recorded {
		row.Recorded = true
		byDir[row.DirName] = row
	}

	var slots []RepoProvenance
	for _, dirName := range PresentDirs(workspaceDir) {
		if row, ok := byDir[dirName]; ok {
			slots = append(slots, row)
			continue
		}
		repo, label := ParseSlotDirName(rs, dirName)
		slot := RepoProvenance{
			DirName:  dirName,
			RepoName: repo,
			Label:    label,
			VCSName:  filepath.Base(workspaceDir),
		}
		if label != "" {
			slot.VCSName = SlotIdentity{
				TaskName: filepath.Base(workspaceDir),
				RepoName: repo,
				Label:    label,
			}.VCSName()
		}
		if rs != nil {
			slot.VCS = rs.DetectVCS(repo)
		}
		slots = append(slots, slot)
	}
	return slots
}

// PresentRepos returns the distinct repos a task's workspace already
// holds, in directory order. A repo held in several slots is reported
// once and a slot directory never counts as a repo of its own, so a
// picker hiding "already present" repos hides exactly the repos that
// are there.
func PresentRepos(rs *RepoSets, workspaceDir string, recorded []RepoProvenance) []string {
	seen := make(map[string]bool)
	var repos []string
	for _, slot := range PresentSlots(rs, workspaceDir, recorded) {
		if slot.RepoName == "" || seen[slot.RepoName] {
			continue
		}
		seen[slot.RepoName] = true
		repos = append(repos, slot.RepoName)
	}
	return repos
}

// ResolveSlotIdentity picks the identity a new working copy of repo
// should be created under. An explicit label is used as given and
// refused if anything already owns the names it implies; an empty label
// takes the initial slot when it is free and otherwise auto-numbers
// (2, 3, …) to the first discriminator nothing has claimed.
func ResolveSlotIdentity(rs *RepoSets, workspaceDir, taskName, repo, label string) (SlotIdentity, error) {
	if label != "" {
		identity := SlotIdentity{TaskName: taskName, RepoName: repo, Label: label}
		if err := identity.Validate(rs); err != nil {
			return SlotIdentity{}, err
		}
		if err := checkSlotFree(rs, workspaceDir, identity); err != nil {
			return SlotIdentity{}, err
		}
		return identity, nil
	}

	var firstConflict error
	for n := 1; n <= maxSlotDiscriminator; n++ {
		candidate := SlotIdentity{TaskName: taskName, RepoName: repo}
		if n > 1 {
			candidate.Label = strconv.Itoa(n)
		}
		if err := candidate.Validate(rs); err != nil {
			if firstConflict == nil {
				firstConflict = err
			}
			continue
		}
		if err := checkSlotFree(rs, workspaceDir, candidate); err != nil {
			if firstConflict == nil {
				firstConflict = err
			}
			continue
		}
		return candidate, nil
	}

	return SlotIdentity{}, fmt.Errorf("no free slot for repo %q in %s: %w",
		repo, filepath.Base(workspaceDir), firstConflict)
}

// SuggestSlotLabel returns a label nothing has claimed for another
// working copy of repo, or "" when the search space is exhausted. It
// exists so a "this repo is already here, name the slot" refusal can
// hand back something that will actually work instead of making the
// caller guess and retry.
//
// The suggestion is a bare discriminator rather than anything semantic:
// krang has no idea what the second checkout is for, and a wrong guess
// dressed up as advice is worse than an obviously mechanical one.
func SuggestSlotLabel(rs *RepoSets, workspaceDir, taskName, repo string) string {
	for n := 2; n <= maxSlotDiscriminator; n++ {
		candidate := SlotIdentity{TaskName: taskName, RepoName: repo, Label: strconv.Itoa(n)}
		if candidate.Validate(rs) != nil {
			continue
		}
		if checkSlotFree(rs, workspaceDir, candidate) != nil {
			continue
		}
		return candidate.Label
	}
	return ""
}

// checkSlotFree refuses an identity whose directory or VCS name
// something already owns.
func checkSlotFree(rs *RepoSets, workspaceDir string, identity SlotIdentity) error {
	dst := SlotDst(rs, workspaceDir, identity)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("slot directory %q already exists in %s",
			identity.DirName(), filepath.Base(workspaceDir))
	}
	return checkVCSNameFree(rs, identity)
}

// checkVCSNameFree refuses an identity whose jj workspace name or git
// branch another working copy already holds. jj would fail on its own,
// but only after creating the directory; git would hand the new
// worktree a branch that may carry another task's unpushed work.
func checkVCSNameFree(rs *RepoSets, identity SlotIdentity) error {
	if rs == nil {
		return nil
	}
	repoSrc := filepath.Join(rs.ReposDir, identity.RepoName)

	switch rs.DetectVCS(identity.RepoName) {
	case "jj":
		// A failure here means the source repo isn't readable as a jj
		// repo; let the creation attempt report that in its own words.
		output, err := runVCSCommand(repoSrc, "jj", "workspace", "list")
		if err != nil {
			return nil
		}
		if !jjWorkspaceListed(output, identity.VCSName()) {
			return nil
		}
		return fmt.Errorf("jj workspace %q already exists in repo %q",
			identity.VCSName(), identity.RepoName)
	default:
		branch := identity.GitBranch()
		output, err := runVCSCommand(repoSrc, "git", "branch", "--list", branch)
		if err != nil {
			return nil
		}
		if strings.TrimSpace(output) == "" {
			return nil
		}
		if identity.Label == "" && reclaimMergedGitBranch(repoSrc, branch) {
			return nil
		}
		if identity.Label == "" {
			return fmt.Errorf(
				"git branch %q in repo %q still holds unmerged work: rename the task, or delete the branch yourself",
				branch, identity.RepoName)
		}
		return fmt.Errorf("git branch %q already exists in repo %q: choose a different slot label",
			branch, identity.RepoName)
	}
}

// reclaimMergedGitBranch takes back a leftover branch belonging to a
// task of the same name — the crashed-task recovery krang has always
// done — but only when git agrees nothing is lost. "git branch -d"
// refuses branches holding unmerged commits and branches checked out
// in a worktree, which is exactly the line to draw: cleanup goes out of
// its way to keep unpushed work, so creation must not throw it away.
func reclaimMergedGitBranch(repoSrc, branch string) bool {
	// A worktree whose directory was deleted without git being told
	// still pins its branch; pruning those entries first is what makes
	// the crashed-task case reclaimable.
	_, _ = runVCSCommand(repoSrc, "git", "worktree", "prune")
	_, err := runVCSCommand(repoSrc, "git", "branch", "-d", branch)
	return err == nil
}

// jjWorkspaceListed reports whether "jj workspace list" output names
// the given workspace. Each line is "<name>: <description>".
func jjWorkspaceListed(output, name string) bool {
	for _, line := range strings.Split(output, "\n") {
		listed, _, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && listed == name {
			return true
		}
	}
	return false
}
