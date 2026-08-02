package task

import (
	"fmt"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/workspace"
)

// CreateSlot gives a task's workspace one more working copy of a repo
// and records where it came from. An empty label takes the repo's
// initial slot when the workspace doesn't hold it yet and auto-numbers
// otherwise; an explicit label is used as given and refused if anything
// already owns the names it implies.
//
// taskID may be empty while a workspace is being built ahead of the
// task row it belongs to. The provenance still comes back on the
// result so the caller can record it with RecordSlot once the task
// exists — every working copy needs a row, because the reconcile
// backfill only fires for tasks with none at all.
//
// base is the revset (jj) or commit-ish (git) to start the working copy
// from. Empty keeps the historical behaviour: detect the remote default
// branch at creation time. Whatever is used ends up in the row's
// base_revision, because a bookmark name recorded after the fact points
// somewhere else.
func (m *Manager) CreateSlot(taskID, taskName, workspaceDir, repo, label, base string) (workspace.CloneRepoResult, error) {
	if m.repoSets == nil {
		return workspace.CloneRepoResult{}, fmt.Errorf("no workspace configuration loaded")
	}

	identity, err := workspace.ResolveSlotIdentity(m.repoSets, workspaceDir, taskName, repo, label)
	if err != nil {
		return workspace.CloneRepoResult{Repo: repo, VCS: m.repoSets.DetectVCS(repo)}, err
	}
	// Set after resolution rather than passed into it: the base has no
	// bearing on which names are free, so letting it into identity
	// resolution would only invite the two to be confused.
	identity.Base = base

	result := workspace.CloneRepoAs(m.repoSets, identity,
		workspace.SlotDst(m.repoSets, workspaceDir, identity))
	if result.Err != nil {
		return result, result.Err
	}

	if taskID != "" {
		if err := m.RecordSlot(taskID, result.Provenance); err != nil {
			return result, err
		}
	}
	return result, nil
}

// RecordSlot persists where one working copy in a task's workspace came
// from, so cleanup forgets the identity krang actually created rather
// than guessing it back out of a directory name.
func (m *Manager) RecordSlot(taskID string, provenance workspace.RepoProvenance) error {
	if m.workspaceRepos == nil {
		return nil
	}
	err := m.workspaceRepos.Create(&db.WorkspaceRepo{
		TaskID:       taskID,
		RepoName:     provenance.RepoName,
		DirName:      provenance.DirName,
		VCS:          provenance.VCS,
		VCSName:      provenance.VCSName,
		SlotLabel:    provenance.Label,
		BaseRevision: provenance.Base,
	})
	if err != nil {
		_ = m.events.Log(taskID, "workspace_repo_record_failed", err.Error())
		return err
	}
	return nil
}
