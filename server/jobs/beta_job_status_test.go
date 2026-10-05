// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package jobs_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
	. "github.com/runatlantis/atlantis/testing"
)

func TestBetaJobStatusLifecycle(t *testing.T) {
	Equals(t, jobs.BetaJobUnknown, jobs.BetaJobStatus(0))
	store := jobs.NewBetaJobStatusStore()
	pull := jobs.PullInfo{PullNum: 42, Repo: "repo", RepoFullName: "org/repo", ProjectName: "project", Path: "dir", Workspace: "default"}
	before := time.Now()
	token := store.Start("job", pull, "plan")
	running := store.Snapshot()["job"]
	Equals(t, "job", running.JobID)
	Equals(t, pull, running.Pull)
	Equals(t, "plan", running.Operation)
	Equals(t, jobs.BetaJobRunning, running.Status)
	Assert(t, !running.StartedAt.Before(before), "start time precedes observation")
	Assert(t, running.FinishedAt.IsZero(), "running observation has a finish time")
	Assert(t, !running.HasPlanStats, "running observation has plan counts")

	stats := models.PlanSuccessStats{Import: 1, Add: 2, Change: 3, Destroy: 4, Forget: 5, Changes: true, ChangesOutside: true}
	store.Finish("job", token, jobs.BetaJobSucceeded, stats, true)
	completed := store.Snapshot()["job"]
	Equals(t, jobs.BetaJobSucceeded, completed.Status)
	Equals(t, running.StartedAt, completed.StartedAt)
	Assert(t, !completed.FinishedAt.Before(completed.StartedAt), "finish precedes start")
	Assert(t, !completed.FinishedAt.After(time.Now()), "finish time is in the future")
	Equals(t, stats, completed.PlanStats)
	Assert(t, completed.HasPlanStats, "successful plan lost its counts")

	// Ordinary completion and abnormal-exit callbacks cannot rewrite a result.
	store.Finish("job", token, jobs.BetaJobFailed, models.PlanSuccessStats{}, false)
	store.Interrupt("job", token)
	Equals(t, completed, store.Snapshot()["job"])

	store.MarkFinalizationFailure("job", token)
	failed := store.Snapshot()["job"]
	Equals(t, jobs.BetaJobFailed, failed.Status)
	Equals(t, completed.FinishedAt, failed.FinishedAt)
	Equals(t, models.PlanSuccessStats{}, failed.PlanStats)
	Assert(t, !failed.HasPlanStats, "finalization failure retained counts")
	store.MarkFinalizationFailure("job", token)
	store.Finish("job", token, jobs.BetaJobSucceeded, stats, true)
	store.Interrupt("job", token)
	Equals(t, failed, store.Snapshot()["job"])
}

func TestBetaJobStatusCounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		status    jobs.BetaJobStatus
		stats     models.PlanSuccessStats
		hasStats  bool
		wantStats bool
	}{
		{"known zero plan", "plan", jobs.BetaJobSucceeded, models.PlanSuccessStats{}, true, true},
		{"unrecognized plan", "plan", jobs.BetaJobSucceeded, models.PlanSuccessStats{Add: 2}, false, false},
		{"failed plan", "plan", jobs.BetaJobFailed, models.PlanSuccessStats{Add: 2}, true, false},
		{"apply", "apply", jobs.BetaJobSucceeded, models.PlanSuccessStats{Add: 2}, true, false},
		{"hook", "pre_workflow", jobs.BetaJobSucceeded, models.PlanSuccessStats{Add: 2}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := jobs.NewBetaJobStatusStore()
			token := store.Start("job", jobs.PullInfo{}, tc.operation)
			store.Finish("job", token, tc.status, tc.stats, tc.hasStats)
			result := store.Snapshot()["job"]
			Equals(t, tc.status, result.Status)
			Equals(t, tc.wantStats, result.HasPlanStats)
			if tc.wantStats {
				Equals(t, tc.stats, result.PlanStats)
			} else {
				Equals(t, models.PlanSuccessStats{}, result.PlanStats)
			}
		})
	}
}

func TestBetaJobStatusGenerations(t *testing.T) {
	store := jobs.NewBetaJobStatusStore()
	pull := jobs.PullInfo{RepoFullName: "org/repo"}
	stats := models.PlanSuccessStats{Add: 2, Changes: true}
	old := store.Start("job", pull, "plan")
	store.Finish("job", old, jobs.BetaJobSucceeded, stats, true)
	current := store.Start("job", pull, "plan")
	running := store.Snapshot()["job"]
	Assert(t, current != old, "restart reused generation")
	Equals(t, models.PlanSuccessStats{}, running.PlanStats)
	Assert(t, !running.HasPlanStats, "restart retained counts")
	Assert(t, running.FinishedAt.IsZero(), "restart retained finish time")
	store.Finish("job", old, jobs.BetaJobFailed, stats, true)
	store.Interrupt("job", old)
	store.MarkFinalizationFailure("job", old)
	store.Finish("job", jobs.BetaJobGeneration{}, jobs.BetaJobSucceeded, stats, true)
	otherStore := jobs.NewBetaJobStatusStore()
	foreign := otherStore.Start("job", pull, "plan")
	store.Finish("job", foreign, jobs.BetaJobSucceeded, stats, true)
	store.Finish("job", current, jobs.BetaJobRunning, stats, true)
	store.Finish("job", current, jobs.BetaJobUnknown, stats, true)
	store.Finish("job", current, jobs.BetaJobStatus(255), stats, true)
	Equals(t, running, store.Snapshot()["job"])
	store.Finish("job", current, jobs.BetaJobSucceeded, stats, true)
	completed := store.Snapshot()["job"]
	store.MarkFinalizationFailure("job", old)
	Equals(t, completed, store.Snapshot()["job"])

	store.Remove(pull)
	store.Finish("job", current, jobs.BetaJobSucceeded, stats, true)
	store.Interrupt("job", current)
	store.MarkFinalizationFailure("job", current)
	Equals(t, 0, len(store.Snapshot()))
	newToken := store.Start("job", pull, "plan")
	Assert(t, newToken != current, "generation reused after cleanup")
	store.Finish("job", newToken, jobs.BetaJobSucceeded, stats, true)
	newResult := store.Snapshot()["job"]
	store.MarkFinalizationFailure("job", current)
	store.Interrupt("job", current)
	store.Finish("job", current, jobs.BetaJobFailed, stats, true)
	Equals(t, newResult, store.Snapshot()["job"])
}

func TestBetaJobStatusInterrupted(t *testing.T) {
	store := jobs.NewBetaJobStatusStore()
	token := store.Start("job", jobs.PullInfo{}, "plan")
	store.MarkFinalizationFailure("job", token)
	Equals(t, jobs.BetaJobRunning, store.Snapshot()["job"].Status)
	store.Interrupt("job", token)
	result := store.Snapshot()["job"]
	Equals(t, jobs.BetaJobUnknown, result.Status)
	Assert(t, !result.FinishedAt.IsZero(), "interruption has no finish time")
	Assert(t, !result.HasPlanStats, "interruption has plan counts")
	store.Finish("job", token, jobs.BetaJobSucceeded, models.PlanSuccessStats{}, true)
	store.MarkFinalizationFailure("job", token)
	store.Interrupt("job", token)
	Equals(t, result, store.Snapshot()["job"])
}

func TestBetaJobStatusSnapshotAndExactCleanup(t *testing.T) {
	store := jobs.NewBetaJobStatusStore()
	pull := jobs.PullInfo{Repo: "repo", RepoFullName: "org/repo", PullNum: 1, ProjectName: "project", Path: "dir", Workspace: "default"}
	identities := []jobs.PullInfo{pull, pull, pull, pull, pull, pull, pull}
	identities[1].Repo = "other"
	identities[2].RepoFullName = "other/repo"
	identities[3].PullNum = 2
	identities[4].ProjectName = "other"
	identities[5].Path = "other"
	identities[6].Workspace = "other"
	for i, identity := range identities {
		store.Start(fmt.Sprint(i), identity, "plan")
	}
	store.Start("same", pull, "apply")
	hook := jobs.PullInfo{Repo: pull.Repo, RepoFullName: pull.RepoFullName, PullNum: pull.PullNum}
	store.Start("hook", hook, "pre_workflow")
	first := store.Snapshot()
	second := store.Snapshot()
	changed := first["1"]
	changed.Pull.Workspace = "mutated"
	changed.PlanStats.Add = 99
	first["1"] = changed
	delete(first, "2")
	first["invented"] = changed
	Equals(t, second, store.Snapshot())
	store.Remove(pull)
	remaining := store.Snapshot()
	Equals(t, 7, len(remaining))
	_, exists := remaining["0"]
	Assert(t, !exists, "cleanup retained first matching job")
	_, exists = remaining["same"]
	Assert(t, !exists, "cleanup retained second matching job")
	Equals(t, 9, len(second)) // Earlier response stays immutable across cleanup.
	store.Remove(hook)
	Equals(t, 6, len(store.Snapshot()))
}

func TestBetaJobStatusNilAndZeroStore(t *testing.T) {
	var absent *jobs.BetaJobStatusStore
	Equals(t, jobs.BetaJobGeneration{}, absent.Start("job", jobs.PullInfo{}, "plan"))
	absent.Finish("job", jobs.BetaJobGeneration{}, jobs.BetaJobSucceeded, models.PlanSuccessStats{}, true)
	absent.Interrupt("job", jobs.BetaJobGeneration{})
	absent.MarkFinalizationFailure("job", jobs.BetaJobGeneration{})
	absent.Remove(jobs.PullInfo{})
	Equals(t, 0, len(absent.Snapshot()))
	var store jobs.BetaJobStatusStore
	Equals(t, jobs.BetaJobGeneration{}, store.Start("", jobs.PullInfo{}, "plan"))
	token := store.Start("job", jobs.PullInfo{}, "plan")
	Assert(t, token != jobs.BetaJobGeneration{}, "zero store declined observation")
	store.Interrupt("job", token)
	Equals(t, jobs.BetaJobUnknown, store.Snapshot()["job"].Status)
}

func TestBetaJobStatusConcurrentObservation(t *testing.T) {
	store := jobs.NewBetaJobStatusStore()
	var workers sync.WaitGroup
	gate := make(chan struct{})
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-gate
			pull := jobs.PullInfo{RepoFullName: "org/repo", ProjectName: fmt.Sprint(worker)}
			for i := range 100 {
				id := fmt.Sprintf("%d/%d", worker, i)
				token := store.Start(id, pull, "plan")
				store.Finish(id, token, jobs.BetaJobSucceeded, models.PlanSuccessStats{Add: i}, true)
				store.MarkFinalizationFailure(id, token)
				store.Interrupt(id, token)
				store.Remove(pull)
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-gate
		for range 200 {
			for id, snapshot := range store.Snapshot() {
				Equals(t, id, snapshot.JobID)
				if snapshot.Status == jobs.BetaJobFailed {
					Assert(t, !snapshot.HasPlanStats, "failed observation has counts")
				}
			}
		}
	}()
	close(gate)
	workers.Wait()
	Equals(t, 0, len(store.Snapshot()))
}

func TestBetaJobStatusCleanupCompletionRace(t *testing.T) {
	store := jobs.NewBetaJobStatusStore()
	pull := jobs.PullInfo{RepoFullName: "org/repo"}
	for range 100 {
		old := store.Start("job", pull, "plan")
		var workers sync.WaitGroup
		gate := make(chan struct{})
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-gate
			store.Remove(pull)
		}()
		go func() {
			defer workers.Done()
			<-gate
			store.Finish("job", old, jobs.BetaJobSucceeded, models.PlanSuccessStats{Add: 2}, true)
			store.MarkFinalizationFailure("job", old)
		}()
		close(gate)
		workers.Wait()
		Equals(t, 0, len(store.Snapshot()))
		current := store.Start("job", pull, "plan")
		store.Finish("job", current, jobs.BetaJobSucceeded, models.PlanSuccessStats{}, true)
		store.MarkFinalizationFailure("job", old)
		Equals(t, jobs.BetaJobSucceeded, store.Snapshot()["job"].Status)
	}
}
