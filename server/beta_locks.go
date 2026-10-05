// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"

	"github.com/runatlantis/atlantis/server/controllers/web_templates"
)

// BetaLocks renders the beta Locks page from the shared dashboard data.
func (s *Server) BetaLocks(w http.ResponseWriter, _ *http.Request) {
	writer := web_templates.NewBetaDashboardTemplate(s.BetaJobStatuses.Snapshot(), web_templates.BetaDashboardLocksPage)
	s.renderIndex(w, writer)
}
