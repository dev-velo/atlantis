# Beta dashboard

Use **Try the beta dashboard** on the classic Atlantis dashboard, or visit
`<atlantis-url>/beta` for Jobs and `<atlantis-url>/beta/locks` for Locks. The
classic dashboard remains available at `/` and both beta pages link back to it.
The old `/beta#locks` bookmark should be replaced with `/beta/locks`; URL
fragments are not sent to the server and cannot be redirected there. No new
server flag or frontend build is needed.

The Jobs section groups tracked output by repository, workspace, and PR/MR.
Lock-only projects appear exclusively in Locks, with their owner and management
links. Locks may supply known owner and PR/MR metadata for existing Jobs entries,
but never create them. Empty job mappings are omitted. The beta does not enumerate
all open VCS requests.
Requests with missing workspace metadata, including workflow hooks, are kept
in an explicitly unavailable workspace group rather than assigned to `default`.

Each output row shows the observed status of that execution: Running, Succeeded,
Failed, or Unknown. Status describes the individual job only; it is not a
request-level result, approval state, or permission to apply. The page is a
server-rendered snapshot, so refresh it to see later status changes. Output must
first be registered by the existing job handler before a row can appear. A
queued command, a silent operation, or a failure before registration may have no
row, and workflow hooks may first appear after they finish.

For compactness, Jobs keeps the first plan and first apply visible, along with
every currently observed Running job. Other executions appear under each
project's **Earlier output** disclosure. The disclosure does not remove entries
from job totals or filtering. Every row's Output link opens that exact execution;
an apply row does not imply which plan it consumed. Displayed times are output
update times and are not guaranteed to reflect execution start order.

On Jobs, ordinary activation opens one bounded output dialog and mounts that
execution's existing terminal page in an iframe. The header repeats that row's
repository, workspace, project, operation, timestamp, and snapshot status/counts.
The full-page Output link remains available for modified clicks, browsers
without JavaScript, and deployments that deny framing. Closing the dialog or
selecting another execution removes the iframe; closing it preserves the Jobs
page's filters and expanded history. A blank or unavailable terminal may mean
the in-memory output expired. The frame's page-load message does not confirm a
WebSocket connection or job success. Status and counts remain server-rendered
snapshots; refresh Jobs to see an updated result.

Successful plans show resource counts only when Atlantis recognizes the plan
summary. `+` means resources to add, `~` means resources to change, and `-`
means resources to destroy; replacements can count as both an add and destroy.
Recognized no-change plans show zero counts and a No changes label. Import and
forget counts are written out when present. Missing, mismatched, or unrecognized
plan metadata omits counts; an all-zero parser result alone is not enough to
claim there were no changes. Counts belong to that plan execution and are not
copied to later applies or other operations.

Observed status and plan counts are kept in memory, not persisted. After a
restart, job status and counts may be Unknown until another execution is
observed, even if its output row remains. The bounded status store retains up to
10,000 entries and may evict older completed observations as it fills; missing
or evicted metadata appears as Unknown. Job output itself is also held in memory
and may disappear after a restart.

Search supports partial names and segment-anchored abbreviations across
repositories, workspaces and known lock owners on the current page.
Space-separated terms and exact dropdown filters combine with AND. User
metadata may be unavailable for jobs without locks. The active page's sidebar
badge and filter summary show matching jobs or locks; Jobs workspace counts
reflect individual tracked jobs, including workflow hooks, rather than request
counts. The inactive page keeps its server-rendered total.
After a restart, Jobs may be empty while persisted Locks remain visible. This
does not discard locks or plans.

Navigation and search/filter controls stay within the viewport while the results
scroll independently. The results region is keyboard-focusable. On short screens,
the filter panel can scroll separately so controls remain reachable without
covering the results.
Job and lock rows use horizontal space for their details, wrapping on
narrow screens. Each job row keeps its operation, status, available counts,
timestamp and output link. Apply controls stays on Jobs. From Locks, its
sidebar link opens `/beta#apply-controls` and brings the enable/disable button
into view, including when the results panel is too short to show the entire
section.
On desktop, the Atlantis version and build information stays in a separate
sidebar footer while navigation scrolls. The Classic dashboard link sits above
the build information, rather than in a page header. It remains available on
mobile, where only the build information is hidden.

Request headings use the stored PR/MR URL when available. Lock rows retain their
project-lock detail link and offer a separate PR/MR link. No provider URL guessing
or VCS API lookup is performed; missing request URLs remain plain text.

Global apply controls use the existing confirmation flow and controllers and
are hidden when global apply locking is disabled. Web authentication, terminal
streaming and existing lock/job routes retain their current behavior. Links and
assets honor the configured Atlantis base path.

## Integration boundary and follow-ups

The beta has a separate Go template, stylesheet and filtering script. The
classic and beta handlers share existing dashboard reads and HTTP 503 responses
for unavailable lock backends. Grouping is a pure view transformation; it does
not change persisted data, job identity, permissions or command execution.

The beta only shows output entries registered by the existing job handler; it
does not synthesize rows from status metadata. A closed output stream alone does
not imply success. User metadata is based on known lock owners and can be
unavailable for jobs without a lock. Request titles and complete author
metadata also need a safe data source before being shown for every request.

The local fixture preview is maintained separately from the production feature.
Before upstream submission, agree on the rollout in an accepted issue and follow
the repository's AI usage policy and DCO requirements.
