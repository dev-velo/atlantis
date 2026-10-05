// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"fmt"
	"testing"

	"github.com/runatlantis/atlantis/server"
	"github.com/runatlantis/atlantis/server/jobs"
	. "github.com/runatlantis/atlantis/testing"
)

type betaCleanupFunc func(jobs.PullInfo)

func (f betaCleanupFunc) CleanUp(pull jobs.PullInfo) { f(pull) }

func TestBetaJobCleanupExactIdentity(t *testing.T) {
	target := jobs.PullInfo{PullNum: 42, Repo: "repo", RepoFullName: "owner/repo", ProjectName: "project", Path: "dir", Workspace: "workspace"}
	identities := []jobs.PullInfo{target, target, target, target, target, target, target, {PullNum: 42, Repo: "repo", RepoFullName: "owner/repo"}}
	identities[1].PullNum++
	identities[2].Repo = "other"
	identities[3].RepoFullName = "other/repo"
	identities[4].ProjectName = "other"
	identities[5].Path = "other"
	identities[6].Workspace = "other"
	store := jobs.NewBetaJobStatusStore()
	for i, pull := range identities {
		store.Start(fmt.Sprint(i), pull, "plan")
	}
	store.Start("another matching execution", target, "apply")
	calls := 0
	cleaner := server.NewBetaJobCleanup(betaCleanupFunc(func(got jobs.PullInfo) {
		calls++
		Equals(t, target, got)
		Equals(t, 9, len(store.Snapshot())) // Delegate before metadata removal.
	}), store)
	cleaner.CleanUp(target)
	Equals(t, 1, calls)
	snapshot := store.Snapshot()
	Equals(t, 7, len(snapshot))
	for i := 1; i < len(identities); i++ {
		Equals(t, identities[i], snapshot[fmt.Sprint(i)].Pull)
	}
}

func TestBetaJobCleanupNilStore(t *testing.T) {
	calls := 0
	server.NewBetaJobCleanup(betaCleanupFunc(func(jobs.PullInfo) { calls++ }), nil).CleanUp(jobs.PullInfo{})
	Equals(t, 1, calls)
}

func TestBetaJobCleanupHookIdentity(t *testing.T) {
	hook := jobs.PullInfo{PullNum: 42, Repo: "repo", RepoFullName: "owner/repo"}
	project := hook
	project.ProjectName = "project"
	project.Path = "dir"
	project.Workspace = "workspace"
	store := jobs.NewBetaJobStatusStore()
	store.Start("pre", hook, "pre plan #0")
	store.Start("post", hook, "post plan #0")
	store.Start("project", project, "plan")
	calls := 0
	server.NewBetaJobCleanup(betaCleanupFunc(func(got jobs.PullInfo) {
		calls++
		Equals(t, hook, got)
	}), store).CleanUp(hook)
	Equals(t, 1, calls)
	Equals(t, 1, len(store.Snapshot()))
	Equals(t, project, store.Snapshot()["project"].Pull)
}
