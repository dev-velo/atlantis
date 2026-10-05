// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"sync"

	"github.com/runatlantis/atlantis/server/events"
	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
)

const betaDeferredObservationLimit = 10_000

// BetaProjectCommandObserver observes existing output-producing executions. JobIDs
// must be unique per execution, as supplied by ProjectCommandContextBuilder.
// Keep the original wrapper's underlying runner intact for cancellation wiring.
type BetaProjectCommandObserver struct {
	*events.ProjectOutputWrapper
	statuses *jobs.BetaJobStatusStore
	mu       sync.Mutex
	pending  map[betaProjectObservationKey]jobs.BetaJobGeneration
	limit    int
}

type betaProjectObservationKey struct {
	jobID     string
	pull      jobs.PullInfo
	operation command.Name
}

var (
	_ events.ProjectCommandRunner         = (*BetaProjectCommandObserver)(nil)
	_ events.DeferredPlanStatusPublisher  = (*BetaProjectCommandObserver)(nil)
	_ events.DeferredApplyStatusPublisher = (*BetaProjectCommandObserver)(nil)
)

// NewBetaProjectCommandObserver wraps project execution without changing the
// original output wrapper or its underlying runner. Nil statuses disable observation.
func NewBetaProjectCommandObserver(wrapper *events.ProjectOutputWrapper, statuses *jobs.BetaJobStatusStore) *BetaProjectCommandObserver {
	return &BetaProjectCommandObserver{ProjectOutputWrapper: wrapper, statuses: statuses, limit: betaDeferredObservationLimit}
}

func betaObservationKey(ctx command.ProjectContext, operation command.Name) betaProjectObservationKey {
	return betaProjectObservationKey{ctx.JobID, jobs.PullInfo{
		PullNum: ctx.Pull.Num, Repo: ctx.BaseRepo.Name, RepoFullName: ctx.BaseRepo.FullName,
		ProjectName: ctx.ProjectName, Path: ctx.RepoRelDir, Workspace: ctx.Workspace,
	}, operation}
}

func (p *BetaProjectCommandObserver) Plan(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.Plan, p.ProjectOutputWrapper.Plan)
}
func (p *BetaProjectCommandObserver) Apply(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.Apply, p.ProjectOutputWrapper.Apply)
}
func (p *BetaProjectCommandObserver) PolicyCheck(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.PolicyCheck, p.ProjectOutputWrapper.PolicyCheck)
}
func (p *BetaProjectCommandObserver) ApprovePolicies(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.ApprovePolicies, p.ProjectOutputWrapper.ApprovePolicies)
}
func (p *BetaProjectCommandObserver) Version(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.Version, p.ProjectOutputWrapper.Version)
}
func (p *BetaProjectCommandObserver) Import(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.Import, p.ProjectOutputWrapper.Import)
}
func (p *BetaProjectCommandObserver) StateRm(ctx command.ProjectContext) command.ProjectCommandOutput {
	return p.observe(ctx, command.State, p.ProjectOutputWrapper.StateRm)
}

func (p *BetaProjectCommandObserver) observe(ctx command.ProjectContext, operation command.Name, execute func(command.ProjectContext) command.ProjectCommandOutput) command.ProjectCommandOutput {
	_, noop := p.JobMessageSender.(*jobs.NoopProjectOutputHandler)
	if p.statuses == nil || ctx.JobID == "" || ctx.SuppressJobOutput || noop {
		return execute(ctx)
	}
	key := betaObservationKey(ctx, operation)
	deferred := operation == command.Apply || (operation == command.Plan && !ctx.API)
	var generation jobs.BetaJobGeneration
	if deferred {
		p.mu.Lock()
		// Early returns in existing workflows can omit final publication. Retain
		// their tokens while metadata exists, then reclaim on admission at capacity. Never
		// evict an outstanding callback just to report another execution successful.
		if len(p.pending) >= p.limit {
			snapshot := p.statuses.Snapshot()
			for old := range p.pending {
				entry, ok := snapshot[old.jobID]
				if !ok || entry.Pull != old.pull || entry.Operation != old.operation.String() {
					delete(p.pending, old)
				}
			}
		}
		if p.pending == nil {
			p.pending = make(map[betaProjectObservationKey]jobs.BetaJobGeneration)
		}
		if len(p.pending) >= p.limit {
			p.mu.Unlock()
			return execute(ctx)
		}
		generation = p.statuses.Start(ctx.JobID, key.pull, operation.String())
		if generation != (jobs.BetaJobGeneration{}) {
			p.pending[key] = generation
		}
		p.mu.Unlock()
	} else {
		generation = p.statuses.Start(ctx.JobID, key.pull, operation.String())
	}
	finished := false
	defer func() {
		if !finished {
			p.statuses.Interrupt(ctx.JobID, generation)
			p.release(key, generation)
		}
	}()
	result := execute(ctx)
	status := jobs.BetaJobSucceeded
	var stats models.PlanSuccessStats
	hasStats := false
	if result.Error != nil || result.Failure != "" {
		status = jobs.BetaJobFailed
	} else if operation == command.Plan && result.PlanSuccess != nil {
		stats = result.PlanSuccess.Stats()
		hasStats = stats.Changes || result.PlanSuccess.NoChanges()
	}
	p.statuses.Finish(ctx.JobID, generation, status, stats, hasStats)
	finished = true
	if status == jobs.BetaJobFailed || (operation == command.Plan && result.PlanSuccess == nil) || (operation == command.Apply && result.ApplySuccess == "") {
		p.release(key, generation)
	}
	return result
}

func (p *BetaProjectCommandObserver) release(key betaProjectObservationKey, generation jobs.BetaJobGeneration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending[key] == generation {
		delete(p.pending, key)
	}
}

func (p *BetaProjectCommandObserver) PublishDeferredPlanStatuses(contexts []command.ProjectContext, result command.Result, status models.CommitStatus) {
	p.ProjectOutputWrapper.PublishDeferredPlanStatuses(contexts, result, status)
	p.finalize(contexts, result, status, command.Plan)
}

func (p *BetaProjectCommandObserver) PublishDeferredApplyStatuses(contexts []command.ProjectContext, result command.Result, status models.CommitStatus) {
	p.ProjectOutputWrapper.PublishDeferredApplyStatuses(contexts, result, status)
	p.finalize(contexts, result, status, command.Apply)
}

func (p *BetaProjectCommandObserver) finalize(contexts []command.ProjectContext, result command.Result, status models.CommitStatus, operation command.Name) {
	for _, res := range result.ProjectResults {
		if res.Command != operation {
			continue
		}
		var key betaProjectObservationKey
		matches := 0
		for _, ctx := range contexts {
			if ctx.CommandName == res.Command && ctx.ProjectName == res.ProjectName && ctx.RepoRelDir == res.RepoRelDir && ctx.Workspace == res.Workspace {
				key = betaObservationKey(ctx, operation)
				matches++
			}
		}
		if matches != 1 {
			continue
		}
		p.mu.Lock()
		generation, ok := p.pending[key]
		if status == models.SuccessCommitStatus || status == models.FailedCommitStatus {
			delete(p.pending, key)
		}
		p.mu.Unlock()
		if ok && status == models.FailedCommitStatus {
			p.statuses.MarkFinalizationFailure(key.jobID, generation)
		}
	}
}
