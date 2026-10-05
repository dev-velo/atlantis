// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/runatlantis/atlantis/server/core/runtime"
	"github.com/runatlantis/atlantis/server/events"
	"github.com/runatlantis/atlantis/server/events/command"
	"github.com/runatlantis/atlantis/server/jobs"
	. "github.com/runatlantis/atlantis/testing"
)

func TestNewServer_BetaJobWiring(t *testing.T) {
	// A TFE token makes the Terraform client write a configuration file in its
	// home directory. Exercise remote mode in a child with an isolated home.
	const remoteEnv = "ATLANTIS_TEST_BETA_REMOTE"
	if os.Getenv(remoteEnv) == "1" {
		testBetaJobWiring(t, true)
		return
	}
	t.Run("async output", func(t *testing.T) { testBetaJobWiring(t, false) })
	t.Run("remote execution", func(t *testing.T) {
		executable, err := os.Executable()
		Ok(t, err)
		child := exec.Command(executable, "-test.run=^TestNewServer_BetaJobWiring$", "-test.v")
		for _, variable := range os.Environ() {
			if !strings.HasPrefix(variable, "HOME=") && !strings.HasPrefix(variable, "PATH=") && !strings.HasPrefix(variable, remoteEnv+"=") {
				child.Env = append(child.Env, variable)
			}
		}
		// Exclude Terraform version-manager shims that depend on the real home.
		child.Env = append(child.Env, "HOME="+t.TempDir(), "PATH=/usr/bin:/bin", remoteEnv+"=1")
		output, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("remote constructor: %v\n%s", err, output)
		}
	})
}

func testBetaJobWiring(t *testing.T, remote bool) {
	t.Helper()
	config := UserConfig{
		DataDir: t.TempDir(), AtlantisURL: "http://example.com", LockingDBType: "boltdb",
		GithubHostname: "github.com", GithubUser: "user", DefaultTFVersion: "1.11.1",
	}
	if remote {
		config.TFEToken = "constructor-test-token"
		config.TFEHostname = "app.terraform.io"
	}
	s, err := NewServer(config, Config{AtlantisVersion: "test"})
	Ok(t, err)
	t.Cleanup(func() { _ = s.StatsCloser.Close() })
	if remote {
		Assert(t, s.BetaJobStatuses == nil, "remote mode must decline beta observation")
		_, ok := s.ProjectCmdOutputHandler.(*jobs.NoopProjectOutputHandler)
		Assert(t, ok, "remote output handler must be Noop")
	} else {
		Assert(t, s.BetaJobStatuses != nil, "async output must have a beta status store")
		_, ok := s.ProjectCmdOutputHandler.(*jobs.AsyncProjectCommandOutputHandler)
		Assert(t, ok, "local output handler must be async")
		Equals(t, 0, len(s.BetaJobStatuses.Snapshot()))
	}

	pre, ok := s.PreWorkflowHooksCommandRunner.PreWorkflowHookRunner.(*BetaWorkflowHookObserver)
	Assert(t, ok, "pre-workflow runner must be observed")
	post, ok := s.PostWorkflowHooksCommandRunner.PostWorkflowHookRunner.(*BetaWorkflowHookObserver)
	Assert(t, ok, "post-workflow runner must be observed")
	Equals(t, s.BetaJobStatuses, pre.statuses)
	Equals(t, s.BetaJobStatuses, post.statuses)
	preRuntime, ok := pre.runner.(runtime.DefaultPreWorkflowHookRunner)
	Assert(t, ok, "pre-workflow runtime runner must be preserved")
	postRuntime, ok := post.runner.(runtime.DefaultPostWorkflowHookRunner)
	Assert(t, ok, "post-workflow runtime runner must be preserved")
	Equals(t, s.ProjectCmdOutputHandler, preRuntime.OutputHandler)
	Equals(t, s.ProjectCmdOutputHandler, postRuntime.OutputHandler)

	// Read constructor dependencies without exporting fields from events solely
	// for tests. Check actual dynamic types and pointer identities in the graph.
	runners := s.CommandRunner.CommentCommandRunnerByCmd
	plan := betaWiringDependency(t, reflect.ValueOf(runners[command.Plan]), "prjCmdRunner", (*events.InstrumentedProjectCommandRunner)(nil))
	observer := betaWiringDependency(t, plan, "projectCommandRunner", (*BetaProjectCommandObserver)(nil))
	Equals(t, reflect.ValueOf(s.BetaJobStatuses).Pointer(), observer.Elem().FieldByName("statuses").Pointer())
	for _, name := range []command.Name{command.Apply, command.ApprovePolicies, command.Import, command.State} {
		instrumented := betaWiringDependency(t, reflect.ValueOf(runners[name]), "prjCmdRunner", (*events.InstrumentedProjectCommandRunner)(nil))
		Equals(t, plan.Pointer(), instrumented.Pointer())
	}
	policyRunner := betaWiringDependency(t, reflect.ValueOf(runners[command.Plan]), "policyCheckCommandRunner", (*events.PolicyCheckCommandRunner)(nil))
	policyInstrumented := betaWiringDependency(t, policyRunner, "prjCmdRunner", (*events.InstrumentedProjectCommandRunner)(nil))
	Equals(t, plan.Pointer(), policyInstrumented.Pointer())
	versionObserver := betaWiringDependency(t, reflect.ValueOf(runners[command.Version]), "prjCmdRunner", (*BetaProjectCommandObserver)(nil))
	Equals(t, observer.Pointer(), versionObserver.Pointer())
	for _, publisher := range []reflect.Type{
		reflect.TypeOf((*events.DeferredPlanStatusPublisher)(nil)).Elem(),
		reflect.TypeOf((*events.DeferredApplyStatusPublisher)(nil)).Elem(),
	} {
		Assert(t, plan.Type().Implements(publisher), "instrumentation must preserve %s", publisher)
		Assert(t, observer.Type().Implements(publisher), "observer must preserve %s", publisher)
	}

	wrapper := observer.Elem().FieldByName("ProjectOutputWrapper")
	Equals(t, reflect.ValueOf(s.ProjectCmdOutputHandler).Pointer(), wrapper.Elem().FieldByName("JobMessageSender").Elem().Pointer())
	cancel, ok := runners[command.Cancel].(*events.CancelCommandRunner)
	Assert(t, ok, "cancel runner must be preserved")
	defaultRunner, ok := cancel.ProjectCmdRunner.(*events.DefaultProjectCommandRunner)
	Assert(t, ok, "cancellation requires the original concrete default runner")
	Equals(t, reflect.ValueOf(defaultRunner).Pointer(), wrapper.Elem().FieldByName("ProjectCommandRunner").Elem().Pointer())
	customRunner, ok := defaultRunner.RunStepRunner.(*runtime.RunStepRunner)
	Assert(t, ok, "custom command runtime runner must be preserved")
	Equals(t, s.ProjectCmdOutputHandler, customRunner.ProjectCmdOutputHandler)
	terraformClient := reflect.ValueOf(customRunner.TerraformExecutor)
	Equals(t, reflect.ValueOf(s.ProjectCmdOutputHandler).Pointer(), terraformClient.Elem().FieldByName("projectCmdOutputHandler").Elem().Pointer())

	closed := betaWiringDependency(t, reflect.ValueOf(s.VCSEventsController.PullCleaner), "cleaner", (*events.PullClosedExecutor)(nil))
	cleanup := betaWiringDependency(t, closed, "LogStreamResourceCleaner", (*BetaJobCleanup)(nil))
	Equals(t, reflect.ValueOf(s.BetaJobStatuses).Pointer(), cleanup.Elem().FieldByName("statuses").Pointer())
	Equals(t, reflect.ValueOf(s.ProjectCmdOutputHandler).Pointer(), cleanup.Elem().FieldByName("cleaner").Elem().Pointer())
}

func betaWiringDependency(t *testing.T, parent reflect.Value, field string, expected any) reflect.Value {
	t.Helper()
	Assert(t, parent.IsValid() && !parent.IsNil(), "missing parent for dependency %q", field)
	value := parent.Elem().FieldByName(field)
	Assert(t, value.IsValid() && !value.IsNil(), "missing dependency %q", field)
	if value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	Equals(t, reflect.TypeOf(expected), value.Type())
	return value
}
