// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"github.com/runatlantis/atlantis/server/core/runtime"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
)

// BetaWorkflowHookObserver observes the complete hook execution, including the
// custom status-file read, without changing its output or error.
type BetaWorkflowHookObserver struct {
	runner   runtime.PreWorkflowHookRunner
	statuses *jobs.BetaJobStatusStore
}

var (
	_ runtime.PreWorkflowHookRunner  = (*BetaWorkflowHookObserver)(nil)
	_ runtime.PostWorkflowHookRunner = (*BetaWorkflowHookObserver)(nil)
)

// NewBetaWorkflowHookObserver wraps either runtime hook interface, whose Run
// signatures are identical. Nil statuses disable observation, including in
// remote-execution mode with a NoopProjectOutputHandler.
func NewBetaWorkflowHookObserver(runner runtime.PreWorkflowHookRunner, statuses *jobs.BetaJobStatusStore) *BetaWorkflowHookObserver {
	return &BetaWorkflowHookObserver{runner: runner, statuses: statuses}
}

func (o *BetaWorkflowHookObserver) Run(ctx models.WorkflowHookCommandContext, command, shell, shellArgs, path string) (string, string, error) {
	if o.statuses == nil || ctx.HookID == "" || ctx.SuppressJobOutput {
		return o.runner.Run(ctx, command, shell, shellArgs, path)
	}
	// Mirror SendWorkflowHook, which intentionally leaves all project fields empty.
	pull := jobs.PullInfo{PullNum: ctx.Pull.Num, Repo: ctx.BaseRepo.Name, RepoFullName: ctx.BaseRepo.FullName}
	generation := o.statuses.Start(ctx.HookID, pull, ctx.HookStepName)
	finished := false
	defer func() {
		if !finished {
			o.statuses.Interrupt(ctx.HookID, generation)
		}
	}()
	output, description, err := o.runner.Run(ctx, command, shell, shellArgs, path)
	status := jobs.BetaJobSucceeded
	if err != nil {
		status = jobs.BetaJobFailed
	}
	o.statuses.Finish(ctx.HookID, generation, status, models.PlanSuccessStats{}, false)
	finished = true
	return output, description, err
}
