// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/runatlantis/atlantis/server/events"
	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
	"github.com/runatlantis/atlantis/server/logging"
	. "github.com/runatlantis/atlantis/testing"
)

type betaObserverRunner struct {
	run func(command.Name, command.ProjectContext) command.ProjectCommandOutput
}

func (r betaObserverRunner) Plan(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.Plan, c)
}
func (r betaObserverRunner) Apply(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.Apply, c)
}
func (r betaObserverRunner) PolicyCheck(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.PolicyCheck, c)
}
func (r betaObserverRunner) ApprovePolicies(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.ApprovePolicies, c)
}
func (r betaObserverRunner) Version(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.Version, c)
}
func (r betaObserverRunner) Import(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.Import, c)
}
func (r betaObserverRunner) StateRm(c command.ProjectContext) command.ProjectCommandOutput {
	return r.run(command.State, c)
}

type betaObserverSender struct{}

func (betaObserverSender) Send(command.ProjectContext, string, bool) {}

type betaObserverSetter struct {
	set func(command.ProjectContext, command.Name, models.CommitStatus, *command.ProjectCommandOutput) error
}

func (s betaObserverSetter) SetJobURLWithStatus(c command.ProjectContext, n command.Name, status models.CommitStatus, out *command.ProjectCommandOutput) error {
	return s.set(c, n, status, out)
}

func betaObserverContext(t *testing.T, id string, operation command.Name) command.ProjectContext {
	return command.ProjectContext{JobID: id, CommandName: operation, BaseRepo: models.Repo{Name: "repo", FullName: "org/repo"}, Pull: models.PullRequest{Num: 7}, ProjectName: "project", RepoRelDir: "infra", Workspace: "production", SuppressVCSStatus: true, Log: logging.NewNoopLogger(t)}
}

func betaObserverFixture(run func(command.Name, command.ProjectContext) command.ProjectCommandOutput) (*BetaProjectCommandObserver, *jobs.BetaJobStatusStore) {
	store := jobs.NewBetaJobStatusStore()
	wrapper := &events.ProjectOutputWrapper{ProjectCommandRunner: betaObserverRunner{run}, JobMessageSender: betaObserverSender{}}
	return NewBetaProjectCommandObserver(wrapper, store), store
}

func betaObserverMethod(p *BetaProjectCommandObserver, operation command.Name) func(command.ProjectContext) command.ProjectCommandOutput {
	switch operation {
	case command.Plan:
		return p.Plan
	case command.Apply:
		return p.Apply
	case command.PolicyCheck:
		return p.PolicyCheck
	case command.ApprovePolicies:
		return p.ApprovePolicies
	case command.Version:
		return p.Version
	case command.Import:
		return p.Import
	case command.State:
		return p.StateRm
	default:
		panic("unsupported operation")
	}
}

func betaObserverResult(c command.ProjectContext, out command.ProjectCommandOutput) command.Result {
	return command.Result{ProjectResults: []command.ProjectResult{{ProjectCommandOutput: out, Command: c.CommandName, ProjectName: c.ProjectName, RepoRelDir: c.RepoRelDir, Workspace: c.Workspace}}}
}

func TestBetaProjectObserverDelegatesAndRecordsRunning(t *testing.T) {
	for _, operation := range []command.Name{command.Plan, command.Apply, command.PolicyCheck, command.ApprovePolicies, command.Version, command.Import, command.State} {
		t.Run(operation.String(), func(t *testing.T) {
			ctx := betaObserverContext(t, "job", command.Version) // actual method determines operation
			want := command.ProjectCommandOutput{PlanSuccess: &models.PlanSuccess{TerraformOutput: "Error: merely text\nPlan: 2 to add, 3 to change, 4 to destroy."}, ApplySuccess: "applied", VersionSuccess: "version"}
			started, release := make(chan struct{}), make(chan struct{})
			calls := 0
			p, store := betaObserverFixture(func(n command.Name, got command.ProjectContext) command.ProjectCommandOutput {
				calls++
				Equals(t, operation, n)
				Equals(t, ctx, got)
				close(started)
				<-release
				return want
			})
			returned := make(chan command.ProjectCommandOutput)
			go func() { returned <- betaObserverMethod(p, operation)(ctx) }()
			<-started
			entry := store.Snapshot()[ctx.JobID]
			Equals(t, jobs.BetaJobRunning, entry.Status)
			Equals(t, operation.String(), entry.Operation)
			Equals(t, betaObservationKey(ctx, operation).pull, entry.Pull)
			close(release)
			Equals(t, want, <-returned)
			Equals(t, 1, calls)
			entry = store.Snapshot()[ctx.JobID]
			Equals(t, jobs.BetaJobSucceeded, entry.Status)
			Equals(t, operation == command.Plan, entry.HasPlanStats)
			Assert(t, !entry.StartedAt.IsZero() && !entry.FinishedAt.IsZero(), "observation needs execution timestamps")
		})
	}
}

func TestBetaProjectObserverFailuresAndPanic(t *testing.T) {
	for _, out := range []command.ProjectCommandOutput{{Error: errors.New("error"), PlanSuccess: &models.PlanSuccess{TerraformOutput: "Plan: 2 to add, 0 to change, 0 to destroy."}}, {Failure: "failure"}} {
		p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { return out })
		ctx := betaObserverContext(t, "failed", command.Plan)
		Equals(t, out, p.Plan(ctx))
		Equals(t, jobs.BetaJobFailed, store.Snapshot()[ctx.JobID].Status)
		Equals(t, false, store.Snapshot()[ctx.JobID].HasPlanStats)
		Equals(t, 0, len(p.pending))
	}
	p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { panic("sentinel") })
	func() {
		defer func() { Equals(t, "sentinel", recover()) }()
		p.Plan(betaObserverContext(t, "panic", command.Plan))
	}()
	Equals(t, jobs.BetaJobUnknown, store.Snapshot()["panic"].Status)
	Equals(t, 0, len(p.pending))
}

func TestBetaProjectObserverBypass(t *testing.T) {
	for _, mode := range []string{"nil", "empty", "suppressed", "noop"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			want := command.ProjectCommandOutput{ApplySuccess: "same"}
			p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { calls++; return want })
			ctx := betaObserverContext(t, "job", command.Apply)
			switch mode {
			case "nil":
				p.statuses = nil
			case "empty":
				ctx.JobID = ""
			case "suppressed":
				ctx.SuppressJobOutput = true
			case "noop":
				p.JobMessageSender = &jobs.NoopProjectOutputHandler{}
			}
			Equals(t, want, p.Apply(ctx))
			Equals(t, 1, calls)
			Equals(t, 0, len(store.Snapshot()))
			Equals(t, 0, len(p.pending))
		})
	}
}

func TestBetaProjectObserverPlanCounts(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		known        bool
		stats        models.PlanSuccessStats
	}{
		{"ordinary", "Plan: 2 to add, 3 to change, 4 to destroy.", true, models.PlanSuccessStats{Add: 2, Change: 3, Destroy: 4, Changes: true}},
		{"zero", "No changes. Your infrastructure matches the configuration.", true, models.PlanSuccessStats{}},
		{"unknown", "custom output", false, models.PlanSuccessStats{}},
		{"multiple and replacement", "Plan: 1 to add, 0 to change, 1 to destroy.\nPlan: 2 to add, 3 to change, 4 to destroy.", true, models.PlanSuccessStats{Add: 3, Change: 3, Destroy: 5, Changes: true}},
		{"import forget", "Plan: 1 to import, 2 to add, 3 to change, 4 to destroy, 5 to forget.", true, models.PlanSuccessStats{Import: 1, Add: 2, Change: 3, Destroy: 4, Forget: 5, Changes: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := command.ProjectCommandOutput{PlanSuccess: &models.PlanSuccess{TerraformOutput: tc.output}}
			p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { return out })
			p.Plan(betaObserverContext(t, "plan", command.Plan))
			entry := store.Snapshot()["plan"]
			Equals(t, tc.known, entry.HasPlanStats)
			Equals(t, tc.stats, entry.PlanStats)
		})
	}
	p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput {
		return command.ProjectCommandOutput{}
	})
	p.Plan(betaObserverContext(t, "empty", command.Plan))
	Equals(t, false, store.Snapshot()["empty"].HasPlanStats)
	Equals(t, 0, len(p.pending))
}

func TestBetaProjectObserverDeferredPublishers(t *testing.T) {
	for _, operation := range []command.Name{command.Plan, command.Apply} {
		for _, finalStatus := range []models.CommitStatus{models.FailedCommitStatus, models.SuccessCommitStatus, models.PendingCommitStatus} {
			t.Run(fmt.Sprintf("%s/%v", operation, finalStatus), func(t *testing.T) {
				out := command.ProjectCommandOutput{PlanSuccess: &models.PlanSuccess{TerraformOutput: "Plan: 2 to add, 0 to change, 0 to destroy."}, ApplySuccess: "applied"}
				p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { return out })
				ctx := betaObserverContext(t, "old", operation)
				betaObserverMethod(p, operation)(ctx)
				newCtx := ctx
				newCtx.JobID = "new"
				betaObserverMethod(p, operation)(newCtx)
				ctx.SuppressVCSStatus = false
				result := betaObserverResult(ctx, out)
				result.Error = errors.New("persistence")
				calls := 0
				p.JobURLSetter = betaObserverSetter{func(got command.ProjectContext, n command.Name, s models.CommitStatus, gotOut *command.ProjectCommandOutput) error {
					calls++
					Equals(t, ctx, got)
					Equals(t, operation, n)
					Equals(t, finalStatus, s)
					if operation == command.Plan {
						Equals(t, command.ProjectCommandOutput{Error: result.Error}, *gotOut)
					} else {
						Equals(t, out, *gotOut)
					}
					return nil
				}}
				before := store.Snapshot()["old"]
				if operation == command.Plan {
					p.PublishDeferredPlanStatuses([]command.ProjectContext{ctx}, result, finalStatus)
				} else {
					p.PublishDeferredApplyStatuses([]command.ProjectContext{ctx}, result, finalStatus)
				}
				Equals(t, 1, calls)
				if finalStatus == models.PendingCommitStatus {
					Equals(t, 2, len(p.pending))
				} else {
					Equals(t, 1, len(p.pending))
				}
				Equals(t, jobs.BetaJobSucceeded, store.Snapshot()["new"].Status)
				entry := store.Snapshot()["old"]
				if finalStatus == models.FailedCommitStatus {
					Equals(t, jobs.BetaJobFailed, entry.Status)
					Equals(t, false, entry.HasPlanStats)
					Equals(t, models.PlanSuccessStats{}, entry.PlanStats)
				} else {
					Equals(t, before, entry)
				}
			})
		}
	}
}

func TestBetaProjectObserverDeferredMatching(t *testing.T) {
	for _, mismatch := range []string{"command", "project", "directory", "workspace", "repository", "request", "id", "ambiguous"} {
		t.Run(mismatch, func(t *testing.T) {
			out := command.ProjectCommandOutput{ApplySuccess: "ok"}
			p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { return out })
			ctx := betaObserverContext(t, "job", command.Apply)
			p.Apply(ctx)
			result := betaObserverResult(ctx, out)
			contexts := []command.ProjectContext{ctx}
			switch mismatch {
			case "command":
				result.ProjectResults[0].Command = command.Plan
			case "project":
				result.ProjectResults[0].ProjectName = "other"
			case "directory":
				result.ProjectResults[0].RepoRelDir = "other"
			case "workspace":
				result.ProjectResults[0].Workspace = "other"
			case "repository":
				contexts[0].BaseRepo.FullName = "other"
			case "request":
				contexts[0].Pull.Num++
			case "id":
				contexts[0].JobID = "other"
			case "ambiguous":
				contexts = append(contexts, ctx)
			}
			p.PublishDeferredApplyStatuses(contexts, result, models.FailedCommitStatus)
			Equals(t, jobs.BetaJobSucceeded, store.Snapshot()["job"].Status)
			Equals(t, 1, len(p.pending))
		})
	}
}

func TestBetaProjectObserverNonterminalPublication(t *testing.T) {
	out := command.ProjectCommandOutput{ApplySuccess: "ok"}
	p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { return out })
	ctx := betaObserverContext(t, "job", command.Apply)
	p.Apply(ctx)
	result := betaObserverResult(ctx, out)
	p.PublishDeferredApplyStatuses([]command.ProjectContext{ctx}, result, models.PendingCommitStatus)
	Equals(t, jobs.BetaJobSucceeded, store.Snapshot()[ctx.JobID].Status)
	Equals(t, 1, len(p.pending))
	p.PublishDeferredApplyStatuses([]command.ProjectContext{ctx}, result, models.FailedCommitStatus)
	Equals(t, jobs.BetaJobFailed, store.Snapshot()[ctx.JobID].Status)
	Equals(t, 0, len(p.pending))
}

func TestBetaProjectObserverPendingCapacityAndCleanup(t *testing.T) {
	out := command.ProjectCommandOutput{ApplySuccess: "ok", PlanSuccess: &models.PlanSuccess{TerraformOutput: "No changes. Your infrastructure matches the configuration."}}
	calls := 0
	p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { calls++; return out })
	p.limit = 1
	ctx := betaObserverContext(t, "retained", command.Apply)
	p.Apply(ctx)
	p.Apply(betaObserverContext(t, "declined", command.Apply))
	Equals(t, 2, calls)
	Equals(t, 1, len(store.Snapshot()))
	Equals(t, 1, len(p.pending))
	// API plans, version, and other immediate operations do not consume capacity.
	api := betaObserverContext(t, "api", command.Plan)
	api.API = true
	p.JobURLSetter = betaObserverSetter{func(command.ProjectContext, command.Name, models.CommitStatus, *command.ProjectCommandOutput) error {
		return nil
	}}
	p.Plan(api)
	Equals(t, jobs.BetaJobSucceeded, store.Snapshot()["api"].Status)
	Equals(t, 1, len(p.pending))
	store.Remove(betaObservationKey(ctx, command.Apply).pull)
	newCtx := betaObserverContext(t, "after-cleanup", command.Apply)
	p.Apply(newCtx)
	Equals(t, 1, len(p.pending))
	Equals(t, jobs.BetaJobSucceeded, store.Snapshot()[newCtx.JobID].Status)
	p.PublishDeferredApplyStatuses([]command.ProjectContext{ctx}, betaObserverResult(ctx, out), models.FailedCommitStatus)
	_, resurrected := store.Snapshot()[ctx.JobID]
	Equals(t, false, resurrected)
	Equals(t, jobs.BetaJobSucceeded, store.Snapshot()[newCtx.JobID].Status)
	p.PublishDeferredApplyStatuses([]command.ProjectContext{newCtx}, betaObserverResult(newCtx, out), models.SuccessCommitStatus)
	Equals(t, 0, len(p.pending))
	p.Apply(betaObserverContext(t, "after-callback", command.Apply))
	Equals(t, 1, len(p.pending))
}

func TestBetaProjectObserverConcurrent(t *testing.T) {
	out := command.ProjectCommandOutput{ApplySuccess: "ok"}
	p, store := betaObserverFixture(func(command.Name, command.ProjectContext) command.ProjectCommandOutput { return out })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		ctx := betaObserverContext(t, fmt.Sprintf("job-%d", i), command.Apply)
		ctx.ProjectName = ctx.JobID
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Apply(ctx)
			store.Snapshot()
			store.Remove(betaObservationKey(ctx, command.Apply).pull)
			p.PublishDeferredApplyStatuses([]command.ProjectContext{ctx}, betaObserverResult(ctx, out), models.FailedCommitStatus)
		}()
	}
	wg.Wait()
	Equals(t, 0, len(store.Snapshot()))
	Equals(t, 0, len(p.pending))
}
