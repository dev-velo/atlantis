// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"github.com/runatlantis/atlantis/server/events"
	"github.com/runatlantis/atlantis/server/jobs"
)

// BetaJobCleanup adds exact-identity metadata removal to the existing output
// cleanup. It is intended only for PullClosedExecutor.LogStreamResourceCleaner.
type BetaJobCleanup struct {
	cleaner  events.ResourceCleaner
	statuses *jobs.BetaJobStatusStore
}

var _ events.ResourceCleaner = (*BetaJobCleanup)(nil)

func NewBetaJobCleanup(cleaner events.ResourceCleaner, statuses *jobs.BetaJobStatusStore) *BetaJobCleanup {
	return &BetaJobCleanup{cleaner: cleaner, statuses: statuses}
}

func (c *BetaJobCleanup) CleanUp(pull jobs.PullInfo) {
	c.cleaner.CleanUp(pull)
	c.statuses.Remove(pull)
}
