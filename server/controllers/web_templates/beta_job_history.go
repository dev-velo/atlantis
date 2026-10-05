// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package web_templates

import (
	"github.com/runatlantis/atlantis/server/jobs"
)

// BetaJobHistoryRow is a presentation-only view of one inventory job.
type BetaJobHistoryRow struct {
	Job        jobs.JobIDInfo
	Operation  string
	Details    BetaJobDetails
	OutputURL  string
	Repository string
	Workspace  string
	Project    string
}

// betaJobHistory partitions a copy of the already ordered inventory. It does
// not alter project.Jobs, which remains the source for filtering and totals.
func betaJobHistory(project BetaProject, pull jobs.PullInfo, details map[string]BetaJobDetails, basePath string) ([]BetaJobHistoryRow, []BetaJobHistoryRow) {
	rows := make([]BetaJobHistoryRow, 0, len(project.Jobs))
	seen := make(map[string]bool, len(project.Jobs))
	for _, job := range project.Jobs {
		if seen[job.JobID] {
			continue
		}
		seen[job.JobID] = true
		jobDetails := details[job.JobID]
		operation := jobDetails.Operation
		if operation == "" {
			operation = betaJobOperation(pull, job.JobStep)
			if operation == "" {
				operation = job.JobStep
			}
		}
		rows = append(rows, BetaJobHistoryRow{
			Job:        job,
			Operation:  operation,
			Details:    jobDetails,
			OutputURL:  basePath + job.JobIDUrl,
			Repository: pull.RepoFullName,
			Workspace:  pull.Workspace,
			Project:    project.Name,
		})
	}
	if len(rows) == 0 {
		return nil, nil
	}

	primary := make([]BetaJobHistoryRow, 0, len(rows))
	selected := make([]bool, len(rows))
	firstPlan, firstApply := false, false
	for i, row := range rows {
		isPlan := row.Operation == "plan"
		isApply := row.Operation == "apply"
		if (isPlan && !firstPlan) || (isApply && !firstApply) || row.Details.Status == jobs.BetaJobRunning {
			selected[i] = true
			primary = append(primary, row)
			if isPlan {
				firstPlan = true
			}
			if isApply {
				firstApply = true
			}
		}
	}
	if len(primary) == 0 {
		selected[0] = true
		primary = append(primary, rows[0])
	}

	earlier := make([]BetaJobHistoryRow, 0, len(rows)-len(primary))
	for i, row := range rows {
		if !selected[i] {
			earlier = append(earlier, row)
		}
	}
	return primary, earlier
}
