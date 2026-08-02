package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/workspace"
)

// The four workspace API operations. Each one runs inside a tea.Cmd
// launched by workspacereq.go, which means it is off the Update loop but
// still the only thing touching the workspace at that moment (for the
// mutating pair — see handleWorkspaceRequest for why reads don't take
// the in-flight slot).
//
// Everything here answers with a hooks.WorkspaceResponse and nothing
// here writes to the model, so an operation is a pure function of the
// database, the repo registry, and what's on disk.

// workspaceListOp enumerates every working copy in a task's workspace.
func (m Model) workspaceListOp(req hooks.WorkspaceRequest, t *db.Task) hooks.WorkspaceResponse {
	if t.WorkspaceDir == "" {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonNoWorkspace, hooks.AppliedNo,
			fmt.Sprintf("task %q has no workspace", t.Name))
	}

	slots := m.workspaceSlots(t)
	resp := hooks.WorkspaceOK(req.Op, map[string]string{
		"workspace_dir": t.WorkspaceDir,
		"strategy":      m.workspaceStrategy(),
	})
	resp.Slots = slots
	return resp
}

// workspaceReposOp lists the repos this metarepo can hand out.
func (m Model) workspaceReposOp(req hooks.WorkspaceRequest, t *db.Task) hooks.WorkspaceResponse {
	if m.repoSets == nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedNo,
			"this krang instance has no krang.yaml, so there is no repo registry to list")
	}

	names, err := m.repoSets.ListRepos()
	if err != nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedNo,
			fmt.Sprintf("reading the repos directory: %v", err))
	}

	// A repo counts as "in the task" if any of its working copies are
	// there. PresentRepos already prefers recorded rows and falls back
	// to the directory scan, which is the same answer the repo picker
	// gives the human.
	held := make(map[string]bool)
	if t.WorkspaceDir != "" {
		for _, repo := range workspace.PresentRepos(m.repoSets, t.WorkspaceDir, m.recordedRepos(t.ID)) {
			held[repo] = true
		}
	}

	setsByRepo := make(map[string][]string)
	for setName, members := range m.repoSets.Sets {
		for _, member := range members {
			setsByRepo[member] = append(setsByRepo[member], setName)
		}
	}

	repos := make([]hooks.RepoInfo, 0, len(names))
	for _, name := range names {
		// An empty slice rather than nil: "sets": [] is one less shape
		// for a caller to handle than "sets": null.
		sets := setsByRepo[name]
		if sets == nil {
			sets = []string{}
		}
		sort.Strings(sets)
		repos = append(repos, hooks.RepoInfo{
			Name:   name,
			InTask: held[name],
			Sets:   sets,
		})
	}

	resp := hooks.WorkspaceOK(req.Op, map[string]string{
		"repos_dir": m.repoSets.ReposDir,
	})
	resp.Repos = repos
	return resp
}

// workspaceAddOp gives the task's workspace one more working copy.
//
// The caller doesn't say whether it wants "a repo I don't have" or
// "another checkout of one I do" — that follows from what the workspace
// already holds. The one thing krang insists on is that the second
// checkout of a repo be named: auto-numbering is fine when a human
// picked repos off a list and can see the result, but an agent that
// gets handed "alpha--2" without asking for it has no idea which of its
// two checkouts is which.
func (m Model) workspaceAddOp(req hooks.WorkspaceRequest, t *db.Task) hooks.WorkspaceResponse {
	if m.repoSets == nil || m.manager == nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedNo,
			"this krang instance has no krang.yaml, so it cannot create working copies")
	}
	if t.WorkspaceDir == "" {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonNoWorkspace, hooks.AppliedNo,
			fmt.Sprintf("task %q has no workspace to add to", t.Name))
	}

	// Shared workspaces are refused rather than guessed at. When two
	// tasks were forked to share one workspace, nothing in krang says
	// which of them owns a slot: the workspace_repos row would name one
	// task, and completing that task would forget a VCS identity the
	// other task is still working in. Refusing keeps the ambiguity
	// visible instead of encoding an arbitrary answer to it.
	if shared, err := m.taskStore.TasksSharingWorkspace(t.WorkspaceDir, t.ID); err == nil && len(shared) > 0 {
		names := make([]string, len(shared))
		for i, s := range shared {
			names[i] = s.Name
		}
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonSharedWorkspace, hooks.AppliedNo,
			fmt.Sprintf("workspace %s is shared with %s, and krang has no owner for a shared workspace's slots: "+
				"the new working copy would be recorded against %q alone and forgotten when %q completes. "+
				"Fork independently instead, or add the repo from a task that owns its workspace.",
				filepath.Base(t.WorkspaceDir), strings.Join(names, ", "), t.Name, t.Name))
	}

	known, err := m.repoSets.ListRepos()
	if err != nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedNo,
			fmt.Sprintf("reading the repos directory: %v", err))
	}
	if !containsString(known, req.Repo) {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownRepo, hooks.AppliedNo,
			fmt.Sprintf("no repo named %q under %s; GET /api/workspace/repos lists what is available",
				req.Repo, m.repoSets.ReposDir))
	}

	slots := m.workspaceSlots(t)
	if req.Label == "" && slotsHoldRepo(slots, req.Repo) {
		suggestion := workspace.SuggestSlotLabel(m.repoSets, t.WorkspaceDir, t.Name, req.Repo)
		message := fmt.Sprintf("task %q already has a working copy of %q; a second one needs a label",
			t.Name, req.Repo)
		if suggestion != "" {
			message += fmt.Sprintf(` (%q is free, giving directory %q)`,
				suggestion, req.Repo+"--"+suggestion)
		}
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonLabelRequired, hooks.AppliedNo, message)
	}

	result, err := m.manager.CreateSlot(t.ID, t.Name, t.WorkspaceDir, req.Repo, req.Label, req.Base)
	if err != nil {
		// A working copy with a directory name got made, so something
		// is on disk even though the operation failed — the failure was
		// in recording it. Say so rather than claiming nothing happened.
		applied := hooks.AppliedNo
		if result.Provenance.DirName != "" {
			applied = hooks.AppliedUnknown
		}
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, applied,
			fmt.Sprintf("adding %q to %s: %v", req.Repo, t.Name, err))
	}

	slot := m.slotInfo(t, result.Provenance, true)
	resp := hooks.WorkspaceOK(req.Op, map[string]string{
		"workspace_dir": t.WorkspaceDir,
		"path":          workspace.SlotPath(m.repoSets, t.WorkspaceDir, slot.Dir, slot.Slot),
	})
	resp.Slot = &slot
	return resp
}

// workspaceRemoveSlotOp takes one working copy back out: forget the VCS
// identity krang recorded for it, remove the directory, drop the row.
//
// Removing the last slot of a repo is not special-cased — it is just how
// a repo leaves a task, and it passes through exactly the same gates.
func (m Model) workspaceRemoveSlotOp(req hooks.WorkspaceRequest, t *db.Task) hooks.WorkspaceResponse {
	if m.repoSets == nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedNo,
			"this krang instance has no krang.yaml, so it cannot clean up working copies")
	}
	if t.WorkspaceDir == "" {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonNoWorkspace, hooks.AppliedNo,
			fmt.Sprintf("task %q has no workspace", t.Name))
	}

	slots := m.workspaceSlots(t)
	target, failure := matchSlot(req, slots)
	if target == nil {
		return failure
	}

	slotPath := workspace.SlotPath(m.repoSets, t.WorkspaceDir, target.Dir, target.Slot)

	// A single_repo task's initial checkout IS the workspace directory,
	// which is also the task's working directory. In multi_repo — the
	// only strategy this API is really for — the task's cwd is the
	// workspace *container* and no slot is ever the cwd root, so this
	// only fires for single_repo. Removing it would be a task teardown
	// wearing a slot removal's clothes, so it is refused outright, force
	// or not: complete the task instead.
	//
	// A cwd that has drifted *into* a slot (Claude cd'd there) is not
	// checked. The agent asking for the removal is the one standing in
	// the directory; refusing on its behalf would make a legitimate
	// "clean this up and move on" impossible.
	if filepath.Clean(slotPath) == filepath.Clean(t.WorkspaceDir) {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonWorkspaceRoot, hooks.AppliedNo,
			fmt.Sprintf("slot %q is task %q's whole workspace directory, not a slot inside it; "+
				"complete the task to tear it down", target.Dir, t.Name))
	}

	if !target.Exists && !req.Force {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonSlotMissing, hooks.AppliedNo,
			fmt.Sprintf("slot %q is recorded but not on disk at %s; somebody removed it outside krang. "+
				`Send {"force": true} to forget its VCS identity and drop the row anyway.`,
				target.Dir, slotPath))
	}

	if !req.Force {
		if blockers := removalBlockers(slotPath, *target); len(blockers) > 0 {
			resp := hooks.WorkspaceFailure(req.Op, hooks.ReasonUnsavedWork, hooks.AppliedNo,
				fmt.Sprintf("removing %q would lose work: %s. Push or commit first, or send {\"force\": true}.",
					target.Dir, describeBlockers(blockers)))
			resp.Blockers = blockers
			return resp
		}
	}

	// Forget the identity krang recorded, not one derived from the
	// directory name: a task holding two slots of one repo has two jj
	// workspaces, and the derivation can only ever name the first.
	forget := workspace.ForgetRepo(m.repoSets, t.WorkspaceDir, workspace.RepoProvenance{
		DirName:  target.Dir,
		RepoName: target.Repo,
		VCS:      target.VCS,
		VCSName:  target.VCSName,
		Label:    target.Slot,
		Recorded: target.Recorded,
	})
	if forget.Err != nil && !req.Force {
		// Stop before touching the directory or the row. Leaving the
		// three in step means the caller can fix the VCS complaint and
		// send the identical request again.
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedNo,
			fmt.Sprintf("forgetting %s working copy %q: %v", target.VCS, target.VCSName, forget.Err))
	}

	if err := os.RemoveAll(slotPath); err != nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedUnknown,
			fmt.Sprintf("removing %s: %v (the VCS identity was already forgotten)", slotPath, err))
	}
	if m.workspaceRepos != nil {
		if err := m.workspaceRepos.DeleteByDir(t.ID, target.Dir); err != nil {
			return hooks.WorkspaceFailure(req.Op, hooks.ReasonOperationFailed, hooks.AppliedYes,
				fmt.Sprintf("the working copy is gone but its provenance row survived: %v", err))
		}
	}

	data := map[string]string{
		"workspace_dir": t.WorkspaceDir,
		"repo_dropped":  fmt.Sprintf("%t", !slotsHoldRepoExcept(slots, target.Repo, target.Dir)),
	}
	if forget.Err != nil {
		data["forget_error"] = forget.Err.Error()
	}
	removed := *target
	removed.Exists = false
	resp := hooks.WorkspaceOK(req.Op, data)
	resp.Slot = &removed
	return resp
}

// matchSlot picks the slot a removal request names. "dir" is exact and
// wins; repo plus label is the friendlier form and must resolve to
// exactly one slot, since an unrecorded directory whose name krang can
// only guess at could otherwise be removed by accident.
func matchSlot(req hooks.WorkspaceRequest, slots []hooks.SlotInfo) (*hooks.SlotInfo, hooks.WorkspaceResponse) {
	if req.Dir != "" {
		for i := range slots {
			if slots[i].Dir == req.Dir {
				return &slots[i], hooks.WorkspaceResponse{}
			}
		}
		return nil, hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownSlot, hooks.AppliedNo,
			fmt.Sprintf("no slot with directory %q; GET /api/workspace lists them (%s)",
				req.Dir, strings.Join(slotDirNames(slots), ", ")))
	}

	var matched []*hooks.SlotInfo
	for i := range slots {
		if slots[i].Repo == req.Repo && slots[i].Slot == req.Label {
			matched = append(matched, &slots[i])
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], hooks.WorkspaceResponse{}
	case 0:
		return nil, hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownSlot, hooks.AppliedNo,
			fmt.Sprintf("no slot of repo %q labeled %q; GET /api/workspace lists them (%s)",
				req.Repo, req.Label, strings.Join(slotDirNames(slots), ", ")))
	default:
		return nil, hooks.WorkspaceFailure(req.Op, hooks.ReasonAmbiguousSlot, hooks.AppliedNo,
			fmt.Sprintf("repo %q labeled %q matches %d directories (%s); name one with \"dir\"",
				req.Repo, req.Label, len(matched), strings.Join(slotDirNames(derefSlots(matched)), ", ")))
	}
}

// removalBlockers names the work removing a slot would destroy.
//
// Only git slots can lose anything. Forgetting a jj workspace leaves its
// commits — including the working-copy commit holding what would be
// "uncommitted changes" anywhere else — in the source repo's store,
// where jj log still finds them. A git worktree removed with --force
// takes its uncommitted changes with it, and while cleanup uses
// "git branch -d" so an unpushed branch survives, that is a subtlety
// worth refusing over rather than relying on.
func removalBlockers(slotPath string, slot hooks.SlotInfo) []hooks.RemovalBlocker {
	if slot.VCS == "jj" {
		return nil
	}
	var blockers []hooks.RemovalBlocker
	if workspace.HasUncommittedChanges(slotPath) {
		blockers = append(blockers, hooks.RemovalBlocker{
			Dir:    slot.Dir,
			Kind:   hooks.BlockerUncommittedChanges,
			Detail: "modified, staged, or untracked files that exist nowhere else",
		})
	}
	if workspace.HasUnpushedCommits(slotPath) {
		blockers = append(blockers, hooks.RemovalBlocker{
			Dir:    slot.Dir,
			Kind:   hooks.BlockerUnpushedCommits,
			Detail: "commits not reachable from any remote branch",
		})
	}
	return blockers
}

func describeBlockers(blockers []hooks.RemovalBlocker) string {
	parts := make([]string, len(blockers))
	for i, b := range blockers {
		parts[i] = b.Kind + " (" + b.Detail + ")"
	}
	return strings.Join(parts, "; ")
}

// workspaceSlots describes every working copy in a task's workspace.
//
// Recorded rows come first, in row order: they are krang's own account
// of what it made, and the only entries carrying a VCS identity and a
// base revision. Repo-looking directories with no row follow, marked
// recorded:false — a checkout somebody made by hand is still part of
// what the workspace holds, and hiding it would make the listing
// disagree with ls.
func (m Model) workspaceSlots(t *db.Task) []hooks.SlotInfo {
	recorded := m.recordedRepos(t.ID)

	infos := make([]hooks.SlotInfo, 0, len(recorded))
	recordedDirs := make(map[string]bool, len(recorded))
	for _, row := range recorded {
		recordedDirs[row.DirName] = true
		infos = append(infos, m.slotInfo(t, row, true))
	}

	// PresentSlots is the filesystem scan with the same repo/label
	// derivation the rest of krang uses for unrecorded directories.
	for _, scanned := range workspace.PresentSlots(m.repoSets, t.WorkspaceDir, recorded) {
		if recordedDirs[scanned.DirName] {
			continue
		}
		infos = append(infos, m.slotInfo(t, scanned, false))
	}
	return infos
}

// slotInfo renders one working copy for the wire.
func (m Model) slotInfo(t *db.Task, p workspace.RepoProvenance, recorded bool) hooks.SlotInfo {
	canonical := ""
	if m.repoSets != nil && p.RepoName != "" {
		canonical = filepath.Join(m.repoSets.ReposDir, p.RepoName)
	}

	_, err := os.Stat(workspace.SlotPath(m.repoSets, t.WorkspaceDir, p.DirName, p.Label))

	return hooks.SlotInfo{
		Dir:               p.DirName,
		Repo:              p.RepoName,
		CanonicalRepoPath: canonical,
		VCS:               p.VCS,
		VCSName:           p.VCSName,
		Slot:              p.Label,
		Base:              p.Base,
		Exists:            err == nil,
		Recorded:          recorded,
	}
}

func (m Model) workspaceStrategy() string {
	if m.repoSets == nil {
		return ""
	}
	return string(m.repoSets.WorkspaceStrategy)
}

func slotDirNames(slots []hooks.SlotInfo) []string {
	names := make([]string, len(slots))
	for i, slot := range slots {
		names[i] = slot.Dir
	}
	return names
}

func derefSlots(slots []*hooks.SlotInfo) []hooks.SlotInfo {
	out := make([]hooks.SlotInfo, len(slots))
	for i, slot := range slots {
		out[i] = *slot
	}
	return out
}

func slotsHoldRepo(slots []hooks.SlotInfo, repo string) bool {
	return slotsHoldRepoExcept(slots, repo, "")
}

// slotsHoldRepoExcept reports whether any slot other than the one in
// exceptDir holds the repo, which is how "was that the last one?" is
// answered after a removal.
func slotsHoldRepoExcept(slots []hooks.SlotInfo, repo, exceptDir string) bool {
	for _, slot := range slots {
		if slot.Dir == exceptDir {
			continue
		}
		if slot.Repo == repo {
			return true
		}
	}
	return false
}

func containsString(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
