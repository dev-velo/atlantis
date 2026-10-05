// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/runatlantis/atlantis/server"
	run "github.com/runatlantis/atlantis/server/core/runtime"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/jobs"
	"github.com/runatlantis/atlantis/server/logging"
	. "github.com/runatlantis/atlantis/testing"
)

type betaHookRunner func(models.WorkflowHookCommandContext, string, string, string, string) (string, string, error)

func (r betaHookRunner) Run(ctx models.WorkflowHookCommandContext, command, shell, args, path string) (string, string, error) {
	return r(ctx, command, shell, args, path)
}

func betaHookContext(t *testing.T) models.WorkflowHookCommandContext {
	return models.WorkflowHookCommandContext{
		HookID: "hook", HookStepName: "pre plan #0", CommandName: "plan",
		BaseRepo: models.Repo{Name: "repo", FullName: "owner/repo"},
		Pull:     models.PullRequest{Num: 42}, ProjectName: "ignored", RepoRelDir: "ignored", Workspace: "ignored",
		Log: logging.NewNoopLogger(t),
	}
}

func TestBetaWorkflowHookObserverResult(t *testing.T) {
	for _, step := range []string{"pre plan #0", "post apply #1"} {
		for _, runErr := range []error{nil, errors.New("hook error")} {
			t.Run(step+"/"+fmtHookError(runErr), func(t *testing.T) {
				ctx := betaHookContext(t)
				ctx.HookStepName = step
				store := jobs.NewBetaJobStatusStore()
				calls := 0
				observer := server.NewBetaWorkflowHookObserver(betaHookRunner(func(got models.WorkflowHookCommandContext, command, shell, args, path string) (string, string, error) {
					calls++
					Equals(t, ctx, got)
					Equals(t, []string{"cmd", "shell", "args", "path"}, []string{command, shell, args, path})
					entry := store.Snapshot()[ctx.HookID]
					Equals(t, jobs.BetaJobRunning, entry.Status)
					Equals(t, jobs.PullInfo{PullNum: 42, Repo: "repo", RepoFullName: "owner/repo"}, entry.Pull)
					Equals(t, step, entry.Operation)
					return "Error: harmless output", "custom status", runErr
				}), store)
				output, desc, err := observer.Run(ctx, "cmd", "shell", "args", "path")
				Equals(t, 1, calls)
				Equals(t, "Error: harmless output", output)
				Equals(t, "custom status", desc)
				Assert(t, err == runErr, "original error must be returned")
				status := jobs.BetaJobSucceeded
				if runErr != nil {
					status = jobs.BetaJobFailed
				}
				entry := store.Snapshot()[ctx.HookID]
				Equals(t, status, entry.Status)
				Assert(t, !entry.HasPlanStats && !entry.FinishedAt.IsZero(), "hooks finish without counts")
			})
		}
	}
}

func fmtHookError(err error) string {
	if err == nil {
		return "success"
	}
	return "failure"
}

func TestBetaWorkflowHookObserverBypass(t *testing.T) {
	for _, mode := range []string{"nil store", "empty id", "suppressed"} {
		t.Run(mode, func(t *testing.T) {
			store := jobs.NewBetaJobStatusStore()
			ctx := betaHookContext(t)
			switch mode {
			case "nil store":
				store = nil
			case "empty id":
				ctx.HookID = ""
			case "suppressed":
				ctx.SuppressJobOutput = true
			}
			calls := 0
			wantErr := errors.New("unchanged")
			observer := server.NewBetaWorkflowHookObserver(betaHookRunner(func(got models.WorkflowHookCommandContext, _, _, _, _ string) (string, string, error) {
				calls++
				Equals(t, ctx, got)
				return "out", "desc", wantErr
			}), store)
			out, desc, err := observer.Run(ctx, "", "", "", "")
			Equals(t, 1, calls)
			Equals(t, "out", out)
			Equals(t, "desc", desc)
			Assert(t, err == wantErr, "original error must be returned")
			Equals(t, 0, len(store.Snapshot()))
		})
	}
}

func TestBetaWorkflowHookObserverAbnormalExit(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			store := jobs.NewBetaJobStatusStore()
			observer := server.NewBetaWorkflowHookObserver(betaHookRunner(func(models.WorkflowHookCommandContext, string, string, string, string) (string, string, error) {
				if mode == "panic" {
					panic("original panic")
				}
				runtime.Goexit()
				return "", "", nil
			}), store)
			done := make(chan any, 1)
			go func() {
				defer func() { done <- recover() }()
				_, _, _ = observer.Run(betaHookContext(t), "", "", "", "")
			}()
			value := <-done
			if mode == "panic" {
				Equals(t, "original panic", value)
			}
			entry := store.Snapshot()["hook"]
			Equals(t, jobs.BetaJobUnknown, entry.Status)
			Assert(t, !entry.FinishedAt.IsZero(), "abnormal exit is terminal")
		})
	}
}

type betaHookOutput struct {
	jobs.NoopProjectOutputHandler
	contexts []models.WorkflowHookCommandContext
}

func (o *betaHookOutput) SendWorkflowHook(ctx models.WorkflowHookCommandContext, _ string, _ bool) {
	o.contexts = append(o.contexts, ctx)
}

func TestBetaWorkflowHookObserverRuntimeErrors(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, command := range []string{"printf success", "printf failure; exit 7", "printf output; mkdir OUTPUT_STATUS_FILE"} {
			t.Run(fmtHookError(nil)+command+map[bool]string{false: "pre", true: "post"}[post], func(t *testing.T) {
				output := &betaHookOutput{}
				var runner run.PreWorkflowHookRunner = run.DefaultPreWorkflowHookRunner{OutputHandler: output}
				if post {
					runner = run.DefaultPostWorkflowHookRunner{OutputHandler: output}
				}
				store := jobs.NewBetaJobStatusStore()
				ctx := betaHookContext(t)
				observer := server.NewBetaWorkflowHookObserver(runner, store)
				out, desc, err := observer.Run(ctx, command, "sh", "-c", t.TempDir())
				Equals(t, "", desc)
				Equals(t, 2, len(output.contexts))
				status := jobs.BetaJobFailed
				if command == "printf success" {
					Ok(t, err)
					Equals(t, "success", out)
					status = jobs.BetaJobSucceeded
				} else {
					Assert(t, err != nil, "command/status-file error must propagate")
					if command == "printf output; mkdir OUTPUT_STATUS_FILE" {
						Equals(t, "output", out)
						Assert(t, strings.Contains(err.Error(), "reading workflow hook status file"), "observe status-file read errors after successful command execution")
					}
				}
				Equals(t, status, store.Snapshot()[ctx.HookID].Status)
			})
		}
	}
}

func TestBetaWorkflowHookObserverNoopMode(t *testing.T) {
	// S4 passes nil statuses when the original output handler is Noop.
	var statuses *jobs.BetaJobStatusStore
	observer := server.NewBetaWorkflowHookObserver(run.DefaultPreWorkflowHookRunner{OutputHandler: &jobs.NoopProjectOutputHandler{}}, statuses)
	out, description, err := observer.Run(betaHookContext(t), "printf output", "sh", "-c", t.TempDir())
	Ok(t, err)
	Equals(t, "output", out)
	Equals(t, "", description)
	Equals(t, 0, len(statuses.Snapshot()))
}

func TestBetaWorkflowHookObserverStaleCompletion(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "replaced"}[replace], func(t *testing.T) {
			store := jobs.NewBetaJobStatusStore()
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			observer := server.NewBetaWorkflowHookObserver(betaHookRunner(func(models.WorkflowHookCommandContext, string, string, string, string) (string, string, error) {
				close(entered)
				<-release
				return "", "", nil
			}), store)
			go func() { defer close(done); _, _, _ = observer.Run(betaHookContext(t), "", "", "", "") }()
			<-entered
			entry := store.Snapshot()["hook"]
			Equals(t, jobs.BetaJobRunning, entry.Status)
			server.NewBetaJobCleanup(betaCleanupFunc(func(jobs.PullInfo) {}), store).CleanUp(entry.Pull)
			if replace {
				store.Start("hook", entry.Pull, entry.Operation)
			}
			close(release)
			<-done
			if replace {
				Equals(t, jobs.BetaJobRunning, store.Snapshot()["hook"].Status)
			} else {
				Equals(t, 0, len(store.Snapshot()))
			}
		})
	}
}

func TestBetaWorkflowHookObserverConcurrent(t *testing.T) {
	store := jobs.NewBetaJobStatusStore()
	observer := server.NewBetaWorkflowHookObserver(betaHookRunner(func(models.WorkflowHookCommandContext, string, string, string, string) (string, string, error) {
		return "", "", nil
	}), store)
	ctx := betaHookContext(t)
	cleaner := server.NewBetaJobCleanup(betaCleanupFunc(func(jobs.PullInfo) {}), store)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_, _, _ = observer.Run(ctx, "", "", "", "")
				cleaner.CleanUp(jobs.PullInfo{PullNum: 42, Repo: "repo", RepoFullName: "owner/repo"})
				store.Snapshot()
			}
		}()
	}
	wg.Wait()
	Equals(t, 0, len(store.Snapshot()))
}
