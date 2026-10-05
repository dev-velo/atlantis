// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package jobs

import (
	"testing"

	"github.com/runatlantis/atlantis/server/events/models"
	. "github.com/runatlantis/atlantis/testing"
)

func TestBetaJobStatusCapacity(t *testing.T) {
	Equals(t, 10_000, NewBetaJobStatusStore().limit)
	store := newBetaJobStatusStore(3)
	pull := PullInfo{RepoFullName: "org/repo"}
	running := store.Start("running", pull, "plan")
	first := store.Start("first", pull, "plan")
	second := store.Start("second", pull, "plan")
	declined := store.Start("declined", pull, "plan")
	Equals(t, BetaJobGeneration{}, declined)
	store.Finish("declined", declined, BetaJobSucceeded, models.PlanSuccessStats{}, true)
	Equals(t, 3, len(store.Snapshot()))

	// Completion order determines age, not start time. All terminal outcomes
	// participate in eviction; the earlier running entry must survive.
	store.Interrupt("second", second)
	store.Finish("first", first, BetaJobFailed, models.PlanSuccessStats{}, false)
	third := store.Start("third", pull, "plan")
	snapshot := store.Snapshot()
	Equals(t, 3, len(snapshot))
	_, exists := snapshot["second"]
	Assert(t, !exists, "oldest terminal entry survived eviction")
	Equals(t, BetaJobRunning, snapshot["running"].Status)
	store.Finish("second", second, BetaJobSucceeded, models.PlanSuccessStats{}, true)
	store.Interrupt("second", second)
	store.MarkFinalizationFailure("second", second)
	Equals(t, snapshot, store.Snapshot())

	// Restarting a terminal ID removes its old eviction position and counts.
	newFirst := store.Start("first", pull, "plan")
	Assert(t, newFirst != first, "restart reused generation")
	Equals(t, BetaJobGeneration{}, store.Start("another", pull, "plan"))
	store.Finish("third", third, BetaJobSucceeded, models.PlanSuccessStats{Add: 1}, true)
	store.MarkFinalizationFailure("third", third)
	store.Start("another", pull, "apply")
	_, exists = store.Snapshot()["third"]
	Assert(t, !exists, "finalization failure entry survived eviction")
	store.Finish("running", running, BetaJobSucceeded, models.PlanSuccessStats{}, false)
	store.Remove(pull)
	Equals(t, 0, len(store.Snapshot()))
	Equals(t, 0, store.terminal.Len())
}

func TestBetaJobStatusZeroCapacity(t *testing.T) {
	store := newBetaJobStatusStore(0)
	Equals(t, BetaJobGeneration{}, store.Start("job", PullInfo{}, "plan"))
	Equals(t, 0, len(store.Snapshot()))
}
