// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package web_templates

import (
	"time"

	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
)

// BetaDashboardPage selects a beta page without changing shared dashboard data.
// The template consumes this selector when the separate Locks page is introduced.
type BetaDashboardPage uint8

const (
	BetaDashboardJobsPage BetaDashboardPage = iota
	BetaDashboardLocksPage
)

// BetaJobDetails describes only this inventory job's matched execution.
// Missing or mismatched metadata has no execution times or counts. Operation
// comes from the structured inventory JobStep even when metadata is unavailable.
type BetaJobDetails struct {
	Operation    string
	Status       jobs.BetaJobStatus
	StartedAt    time.Time
	FinishedAt   time.Time
	PlanStats    models.PlanSuccessStats
	HasPlanStats bool
}

// NewBetaDashboardTemplate captures a snapshot for one response. It owns a copy
// so later caller mutations cannot change rendering, and never reads the store.
// An unrecognized page selector falls back to Jobs, the zero-value default.
func NewBetaDashboardTemplate(snapshot map[string]jobs.BetaJobSnapshot, page BetaDashboardPage) TemplateWriter {
	if page != BetaDashboardLocksPage {
		page = BetaDashboardJobsPage
	}
	copied := make(map[string]jobs.BetaJobSnapshot, len(snapshot))
	for id, details := range snapshot {
		copied[id] = details
	}
	return betaDashboardWriter{snapshot: copied, page: page}
}

func betaJobDetails(data IndexData, snapshot map[string]jobs.BetaJobSnapshot) map[string]BetaJobDetails {
	result := make(map[string]BetaJobDetails)
	// Normally IDs are unique. If malformed inventory repeats one ID across
	// identities, a single details-map entry must not expose another row's result.
	type identity struct {
		pull      jobs.PullInfo
		operation string
	}
	seen := make(map[string]identity)
	conflicts := make(map[string]bool)
	for _, entry := range data.PullToJobMapping {
		for _, job := range entry.JobIDInfos {
			operation := betaJobOperation(entry.Pull, job.JobStep)
			current := identity{entry.Pull, operation}
			if previous, exists := seen[job.JobID]; exists && previous != current {
				conflicts[job.JobID] = true
			}
			seen[job.JobID] = current
			details := BetaJobDetails{Operation: operation}
			observed, exists := snapshot[job.JobID]
			if !conflicts[job.JobID] && exists && job.JobID != "" && operation != "" &&
				observed.JobID == job.JobID && observed.Pull == entry.Pull && observed.Operation == operation {
				switch observed.Status {
				case jobs.BetaJobUnknown, jobs.BetaJobRunning, jobs.BetaJobSucceeded, jobs.BetaJobFailed:
					details.Status = observed.Status
					details.StartedAt = observed.StartedAt
					details.FinishedAt = observed.FinishedAt
				}
				if details.Status == jobs.BetaJobSucceeded && operation == command.Plan.String() && observed.HasPlanStats {
					details.PlanStats = observed.PlanStats
					details.HasPlanStats = true
				}
			}
			result[job.JobID] = details
		}
	}
	return result
}

func betaJobOperation(pull jobs.PullInfo, step string) string {
	if pull.ProjectName == "" && pull.Path == "" && pull.Workspace == "" {
		// SendWorkflowHook uses ctx.HookStepName verbatim, with empty project
		// fields. Do not parse the hook's free-form description or invent a name.
		return step
	}
	for _, name := range []command.Name{command.Plan, command.Apply, command.PolicyCheck, command.ApprovePolicies, command.Version, command.Import, command.State} {
		if step == name.String() {
			return name.String()
		}
	}
	return ""
}
