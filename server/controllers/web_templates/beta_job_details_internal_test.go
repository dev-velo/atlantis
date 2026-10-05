// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package web_templates

import (
	"bytes"
	"testing"
	"time"

	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
	. "github.com/runatlantis/atlantis/testing"
)

func betaDetailsFixture() (IndexData, jobs.BetaJobSnapshot) {
	pull := jobs.PullInfo{Repo: "repo", RepoFullName: "org/repo", PullNum: 42, ProjectName: "infra", Path: "terraform/infra", Workspace: "prod"}
	data := IndexData{CleanedBasePath: "/atlantis", PullToJobMapping: []jobs.PullInfoWithJobIDs{{Pull: pull, JobIDInfos: []jobs.JobIDInfo{{JobID: "job", JobStep: "plan", JobIDUrl: "/jobs/job", JobDescription: "untrusted description"}}}}}
	observed := jobs.BetaJobSnapshot{JobID: "job", Pull: pull, Operation: "plan", Status: jobs.BetaJobSucceeded, StartedAt: time.Unix(100, 0), FinishedAt: time.Unix(200, 0), HasPlanStats: true,
		PlanStats: models.PlanSuccessStats{Add: 2, Change: 3, Destroy: 4, Import: 5, Forget: 6, Changes: true}}
	return data, observed
}

func TestBetaJobDetailsExactJoin(t *testing.T) {
	data, observed := betaDetailsFixture()
	details := betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"]
	Equals(t, BetaJobDetails{Operation: "plan", Status: jobs.BetaJobSucceeded, StartedAt: observed.StartedAt, FinishedAt: observed.FinishedAt, PlanStats: observed.PlanStats, HasPlanStats: true}, details)
	for _, tc := range []struct {
		name   string
		mutate func(*jobs.BetaJobSnapshot)
	}{
		{"job ID", func(s *jobs.BetaJobSnapshot) { s.JobID = "other" }},
		{"repo name", func(s *jobs.BetaJobSnapshot) { s.Pull.Repo = "other" }},
		{"repo full name", func(s *jobs.BetaJobSnapshot) { s.Pull.RepoFullName = "other/repo" }},
		{"request", func(s *jobs.BetaJobSnapshot) { s.Pull.PullNum++ }},
		{"project", func(s *jobs.BetaJobSnapshot) { s.Pull.ProjectName = "other" }},
		{"directory", func(s *jobs.BetaJobSnapshot) { s.Pull.Path = "other" }},
		{"workspace", func(s *jobs.BetaJobSnapshot) { s.Pull.Workspace = "other" }},
		{"operation", func(s *jobs.BetaJobSnapshot) { s.Operation = "apply" }},
		{"invalid status", func(s *jobs.BetaJobSnapshot) { s.Status = jobs.BetaJobStatus(255) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := observed
			tc.mutate(&changed)
			Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": changed})["job"])
		})
	}
	Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, nil)["job"])
	Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"other": observed})["job"])
	data.PullToJobMapping[0].JobIDInfos[0].JobID = ""
	observed.JobID = ""
	Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"": observed})[""])
}

func TestBetaJobDetailsOperationsAndCounts(t *testing.T) {
	for _, operation := range []string{"plan", "apply", "policy_check", "approve_policies", "version", "import", "state"} {
		for _, status := range []jobs.BetaJobStatus{jobs.BetaJobUnknown, jobs.BetaJobRunning, jobs.BetaJobSucceeded, jobs.BetaJobFailed} {
			data, observed := betaDetailsFixture()
			data.PullToJobMapping[0].JobIDInfos[0].JobStep = operation
			observed.Operation, observed.Status = operation, status
			details := betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"]
			Equals(t, operation, details.Operation)
			Equals(t, status, details.Status)
			hasCounts := operation == "plan" && status == jobs.BetaJobSucceeded
			Equals(t, hasCounts, details.HasPlanStats)
			if !hasCounts {
				Equals(t, models.PlanSuccessStats{}, details.PlanStats)
			}
		}
	}
	data, observed := betaDetailsFixture()
	observed.PlanStats = models.PlanSuccessStats{}
	Equals(t, true, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"].HasPlanStats) // Recognized no-change plan.
	observed.HasPlanStats = false
	Equals(t, false, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"].HasPlanStats)
	for _, step := range []string{"", "custom plan", "unlock", "cancel"} {
		data.PullToJobMapping[0].JobIDInfos[0].JobStep = step
		data.PullToJobMapping[0].JobIDInfos[0].JobDescription = "plan"
		observed.Operation = step
		Equals(t, BetaJobDetails{}, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"])
	}
}

func TestBetaJobDetailsHooksAndInventory(t *testing.T) {
	data, observed := betaDetailsFixture()
	for _, step := range []string{"pre plan #0", "post apply #12"} {
		hook := jobs.PullInfo{Repo: "repo", RepoFullName: "org/repo", PullNum: 42}
		data.PullToJobMapping[0].Pull = hook
		data.PullToJobMapping[0].JobIDInfos[0].JobStep = step
		observed.Pull, observed.Operation = hook, step
		details := betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"]
		Equals(t, step, details.Operation)
		Equals(t, jobs.BetaJobSucceeded, details.Status)
		Equals(t, false, details.HasPlanStats)
		observed.Pull.Workspace = "default"
		Equals(t, BetaJobDetails{Operation: step}, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"])
	}
	data, observed = betaDetailsFixture()
	data.Locks = []LockIndexData{{RepoFullName: "lock/repo", PullNum: 2, Workspace: "prod"}}
	data.PullToJobMapping = append(data.PullToJobMapping, jobs.PullInfoWithJobIDs{Pull: jobs.PullInfo{RepoFullName: "empty/repo"}})
	details := betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed, "store-only": {JobID: "store-only", Status: jobs.BetaJobRunning}})
	Equals(t, 1, len(details))
	Equals(t, 1, GroupBetaDashboard(data).Total)
	duplicate := data.PullToJobMapping[0]
	duplicate.Pull.Workspace = "other"
	data.PullToJobMapping = append(data.PullToJobMapping, duplicate)
	Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, map[string]jobs.BetaJobSnapshot{"job": observed})["job"])
}

func TestBetaDashboardSnapshotWriter(t *testing.T) {
	data, observed := betaDetailsFixture()
	snapshot := map[string]jobs.BetaJobSnapshot{"job": observed}
	writer := NewBetaDashboardTemplate(snapshot, BetaDashboardLocksPage).(betaDashboardWriter)
	delete(snapshot, "job")
	Equals(t, jobs.BetaJobSucceeded, betaJobDetails(data, writer.snapshot)["job"].Status)
	other := NewBetaDashboardTemplate(nil, BetaDashboardJobsPage).(betaDashboardWriter)
	Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, other.snapshot)["job"])
	Equals(t, jobs.BetaJobSucceeded, betaJobDetails(data, writer.snapshot)["job"].Status)
	Equals(t, BetaDashboardLocksPage, writer.page)
	Equals(t, BetaDashboardJobsPage, NewBetaDashboardTemplate(nil, BetaDashboardPage(255)).(betaDashboardWriter).page)
	Equals(t, BetaDashboardJobsPage, BetaDashboardTemplate.(betaDashboardWriter).page)
	Equals(t, BetaJobDetails{Operation: "plan"}, betaJobDetails(data, BetaDashboardTemplate.(betaDashboardWriter).snapshot)["job"])
	var defaultHTML, snapshotHTML bytes.Buffer
	Ok(t, BetaDashboardTemplate.Execute(&defaultHTML, data))
	Ok(t, writer.Execute(&snapshotHTML, data))
	Equals(t, defaultHTML.String(), snapshotHTML.String()) // S5 adds data only; L1/L2 own presentation.
	Assert(t, writer.Execute(&snapshotHTML, nil) != nil, "invalid input must return an error")
	Equals(t, data, GroupBetaDashboard(data).IndexData)
}
