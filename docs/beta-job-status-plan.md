# Beta dashboard jobs, locks, and output: implementation plan

Status: S1–S5 and L1–L4 implemented locally; R1 remains pending. L4 browser
verification remains outstanding.
Observers are wired into Atlantis, and Jobs rows show per-execution status and
recognized plan counts. Separate Jobs and Locks pages and compact history are
implemented. Browser verification of the output dialog remains pending.

Prepared against local `dev-ui` at `b4c13bbc`. Recheck the named integration
points before implementing if the branch has moved.

## S1 implementation handoff

Implemented by GPT-6.1 Sol / Medium in three new files:
`server/jobs/beta_job_status.go`, `server/jobs/beta_job_status_test.go`, and
`server/jobs/beta_job_status_internal_test.go`. No existing application files
changed. Nothing committed, pushed, or restarted.

The exported store API is:

```go
NewBetaJobStatusStore() *BetaJobStatusStore
Start(jobID string, pull PullInfo, operation string) BetaJobGeneration
Finish(jobID string, generation BetaJobGeneration, status BetaJobStatus, stats models.PlanSuccessStats, hasStats bool)
Interrupt(jobID string, generation BetaJobGeneration)
MarkFinalizationFailure(jobID string, generation BetaJobGeneration)
Snapshot() map[string]BetaJobSnapshot
Remove(pull PullInfo)
```

Except for the constructor, these are methods on `*BetaJobStatusStore`. Nil
receivers and a zero-value store are supported. Status values are
`BetaJobUnknown` (zero), `BetaJobRunning`, `BetaJobSucceeded`, and `BetaJobFailed`.
The zero generation token means observation was declined. S2 must retain the
token returned by Start through deferred finalization; do not introduce a
job-ID-only update that bypasses stale-generation protection. The operation for
plan counts is `"plan"`. Eviction uses terminal-transition order, not start time;
finalization failure preserves finish time and eviction position.

Validation passed: `go test ./server/jobs`, race tests for `^TestBetaJobStatus`,
20 repeated runs of those tests, `PATH=/opt/homebrew/bin:$PATH make check-fmt`,
and `go build -o /private/tmp/atlantis-s1-build .`. The actual module/toolchain was
Go 1.27.1. No outstanding S1 blockers. Next authorized task must be requested
separately.

## S2 implementation handoff

Implemented by GPT-6.1 Sol / Medium in two new files:
`server/beta_job_observer.go` and `server/beta_job_observer_internal_test.go`.
No existing application files changed. The user approved reliance on the
existing fresh UUID per execution for deferred callback correlation.

S4 should construct the observer with
`NewBetaProjectCommandObserver(wrapper *events.ProjectOutputWrapper,
statuses *jobs.BetaJobStatusStore) *BetaProjectCommandObserver`. Place it outside
the original `ProjectOutputWrapper` for the instrumented and version runners;
leave that wrapper's underlying default runner intact for cancellation.
Operation strings are `plan`, `apply`, `policy_check`, `approve_policies`,
`version`, `import`, and `state` (the `StateRm` method).

The observer retains at most 10,000 metadata-only generation records keyed by
the unique JobID, exact project identity, and operation. Terminal deferred
callbacks release records; pending callbacks retain them. Failures, panics, and
results ineligible for deferred publication release records immediately. API
plans and other immediate operations consume no deferred capacity. On admission
at capacity, removed or mismatched store entries are pruned; if still full, the
new deferred observation is declined while execution proceeds. If a workflow
never publishes a deferred callback, its record stays while matching metadata
remains. No raw output, errors, or result pointers are retained.

Validation passed: focused observer tests, `go test ./server/jobs ./server/events
./server/controllers/web_templates ./server`, ten race-test repetitions for S1/S2,
`PATH=/opt/homebrew/bin:$PATH make check-fmt`, and a build to
`/private/tmp/atlantis-s2-build`. No outstanding S2 blockers. S3 is next in the
assigned order.

## S3 implementation handoff

Implemented by GPT-6.1 Sol / Medium in four new files:
`server/beta_workflow_hook_observer.go`,
`server/beta_workflow_hook_observer_test.go`, `server/beta_job_cleanup.go`, and
`server/beta_job_cleanup_test.go`. No existing application files changed.

S4 constructor signatures are
`NewBetaWorkflowHookObserver(runner runtime.PreWorkflowHookRunner,
statuses *jobs.BetaJobStatusStore) *BetaWorkflowHookObserver` and
`NewBetaJobCleanup(cleaner events.ResourceCleaner,
statuses *jobs.BetaJobStatusStore) *BetaJobCleanup`. The hook observer implements
both runtime pre/post hook interfaces; wrap the original injected runner for
each and pass nil statuses in Noop mode. Its operation is `ctx.HookStepName`,
which matches the existing hook inventory. Its identity mirrors
`SendWorkflowHook`, leaving project, directory, and workspace empty. The
cleanup wrapper calls the original cleaner once before exact-identity metadata
removal; wire it only into `PullClosedExecutor.LogStreamResourceCleaner`.

Validation passed: focused S3 tests, `go test ./server/jobs ./server/events
./server/controllers/web_templates ./server`, five race-test repetitions across
S1–S3, `PATH=/opt/homebrew/bin:$PATH make check-fmt`, and a build to
`/private/tmp/atlantis-s3-build`. No outstanding S3 blockers. S4 is next.

## S4 implementation handoff

Implemented by GPT-6.1 Sol / Medium with narrow edits in `server/server.go`
and a new `server/beta_job_wiring_internal_test.go`. Async output uses one
shared status store; remote Noop output keeps it nil. The project observer sits
outside the original output wrapper for instrumented and version runners.
Pre/post hook observers and the pull-closed log cleanup wrapper share the same
store. Cancellation still receives the original concrete default runner.

S5 may call `s.BetaJobStatuses.Snapshot()` directly; the method accepts a nil
receiver and returns an empty snapshot. Existing tests that construct a Server
without the optional field remain valid.

Focused constructor tests, four-package regression, targeted race tests,
`make check-fmt`, `make build-service`, and `git diff --check` passed with Go
1.27.1. No outstanding S4 blockers. S5 is next.

## S5 implementation handoff

Implemented by GPT-6.1 Sol / Medium with a new
`server/controllers/web_templates/beta_job_details.go` and internal tests, plus
narrow edits to the existing beta writer and `server/beta_dashboard.go`. No
template, asset, route, shared model, or constructor changes were needed.

The writer factory is
`NewBetaDashboardTemplate(snapshot map[string]jobs.BetaJobSnapshot,
page BetaDashboardPage) TemplateWriter`. It copies the snapshot into one writer
instance; `Server.BetaDashboard` supplies one store snapshot to that writer and
continues to use `renderIndex`. The zero-value `BetaDashboardTemplate` remains
a valid default and existing rendered HTML is unchanged.

The template data has `BetaDashboardData.Page`, with
`BetaDashboardJobsPage = 0` and `BetaDashboardLocksPage = 1`; invalid page
values default to Jobs. L2 can use this selector to render only the chosen
section. `BetaDashboardData.JobDetails` is a
`map[string]BetaJobDetails` keyed by each inventory job ID. Details contain
`Operation`, `Status`, `StartedAt`, `FinishedAt`, `PlanStats`, and
`HasPlanStats`. Project operations are `plan`, `apply`, `policy_check`,
`approve_policies`, `version`, `import`, and `state`. Hook operations use the
exact structured `HookStepName`, for example `pre plan #0`.

The join verifies job ID, full project identity, and operation. Absent,
mismatched, empty-ID, conflicting duplicate, or malformed status metadata
produces Unknown with zero times/counts; the operation can retain a safe
structured inventory value. Counts appear in data only for matched successful
plans with recognized stats. The template has not yet been taught to show them.

Focused tests, four-package regression without skips, targeted race tests,
`make check-fmt`, a build to `/private/tmp/atlantis-s5-build`, and
`git diff --check` passed. No outstanding S5 blockers.

## L1 implementation handoff

Implemented by GPT-6 Luna / High in `beta-dashboard.html.tmpl`,
`beta-dashboard.css`, and `docs/beta-dashboard.md`. Status labels use only the
known enum values; unknown metadata displays Unknown. Recognized successful
plans display `+N ~N -N`, spoken resource-action text, import/forget counts when
nonzero, and a No changes label for recognized zeros. Unavailable counts remain
omitted. The user-requested search help, visible Jobs heading/description, and
data note were removed while preserving the hidden accessible heading and
Search placeholder. The filter JavaScript and its cache revision stayed
unchanged; only the CSS revision advanced to v4.

Updated the existing template test's expected hidden Jobs heading and CSS
revision. Per current task instruction, no tests or build were run; the agent
reviewed only its scoped diff. The existing test source change is unverified.
No L1 implementation blocker was reported. L2 is next in the assigned order.

## L2 implementation handoff

Implemented by GPT-6 Luna / High with a new `server/beta_locks.go` handler and
narrow edits to `server/server.go`, the beta template, its filter JavaScript and
CSS, and `docs/beta-dashboard.md`.
`GET /beta` selects the Jobs page and `GET /beta/locks` selects the Locks page
through the existing S5 writer factory. Both use `renderIndex`, retaining shared
lock and job reads. The template renders only the selected content section,
server-selects its page title, accessible heading and sidebar state, and leaves
Apply controls on Jobs. The Locks page's Apply controls link targets
`/beta#apply-controls` under the configured base path.

Filtering now builds dropdowns and rows from the active page only, guards page
specific empty-state and lock-list selectors, updates only the active sidebar
count, and reports only that page's filtered total. The fuzzy matcher is
unchanged. CSS and JavaScript asset revisions advanced to v5 and v3. The docs
identify `/beta/locks` as the replacement for `/beta#locks` bookmarks.

Per task instruction, no tests or builds were run and no new tests were added.
`git diff --check` passed. No implementation blocker was identified; runtime and
test behavior remain unverified.

## L3 implementation handoff

Implemented by GPT-6 Luna / High with a new
`server/controllers/web_templates/beta_job_history.go`, derived presentation
fields in `server/controllers/web_templates/beta_dashboard.go`, and compact row
and disclosure rendering in the beta template and stylesheet. `BetaProject.Jobs`
remains the complete, sorted inventory used for totals and filtering. The writer
derives primary rows (first plan/apply plus every matched Running job) and an
Earlier output disclosure without changing that inventory. If no row qualifies,
the first inventory row stays visible. Duplicate job IDs within a project are
rendered once.

Each rendered row has one exact-job Output link, operation, status, plan counts
when available, and the existing output-update timestamp. Operation comes from
matched details or structured `JobStep`; descriptions and terminal content are
not used. Nested disclosure styling overrides repository-summary styling.
The CSS cache revision advanced to v6.

Per task instruction, no tests were added or run. No build or runtime/browser
verification was run. This handoff is implemented but unverified; L4 is next.

## Model assignments and execution boundaries

These assignments are engineering recommendations for this plan, not guarantees
of model performance. Use **GPT-6 Luna / High** for the UI tasks and
**GPT-6.1 Sol / Medium** for backend correctness, the data handoff, and review.
This reserves the stronger model for concurrency and execution-boundary risks.
It follows the distinction between focused coding and complex coding in the
[official model guidance](https://learn.chatgpt.com/docs/models).

| Task ID | Section / deliverable | Assigned model | Prerequisites |
| --- | --- | --- | --- |
| S1 | 1. Metadata store and concurrency tests | GPT-6.1 Sol / Medium | None |
| S2 | 2–3. Project observer, deferred publishers, plan counts, and tests | GPT-6.1 Sol / Medium | S1 |
| S3 | 4. Hook observers, cleanup, and tests | GPT-6.1 Sol / Medium | S1 |
| S4 | 5. Constructor wiring and regression checks | GPT-6.1 Sol / Medium | S2, S3 |
| S5 | 6. Snapshot join, writer factory, page selector, and tests | GPT-6.1 Sol / Medium | S4 |
| L1 | 7. Badges, counts, text cleanup, and related docs/tests | GPT-6 Luna / High | S5 |
| L2 | 8. Separate pages, navigation, filter guards, and tests | GPT-6 Luna / High | L1 |
| L3 | 9. Compact history helper, rendering, and tests | GPT-6 Luna / High | L2 |
| L4 | 10. Output dialog, assets, and browser verification | GPT-6 Luna / High | L3 |
| R1 | Final integrated review and verification below | GPT-6.1 Sol / Medium | S1–S5 and L1–L4 |

Execute sequentially in table order on the same up-to-date branch. Although some
dependencies are independent, do not run editing agents concurrently: the beta
adapter, template, and `server.go` are shared integration files. A task assignment
authorizes only the named section's implementation, necessary integration edits,
associated tests, and documentation. It does not authorize other tasks merely
because they touch the same file.

Before each task, read the whole plan for constraints, inspect current changes,
and verify prerequisites from the actual code and tests. Stop and report a
missing prerequisite or contract mismatch; do not implement another model's
task, invent replacement interfaces, or silently broaden the plan. In particular,
Luna must not change execution observers, status semantics, store locking,
cancellation wiring, or the shared job/database/terminal model to make UI work.
L2 may add its one route registration in `server.go`, but not change constructors.

Each implementer owns validation for its task; R1 is not a substitute for tests.
At each handoff, report completed task IDs, files changed, checks run and their
results, and remaining blockers. S5 must also give Luna the actual writer-factory
signature, page-selector values, details-map fields, operation/status values,
and missing-metadata behavior. No placeholder success data or generated fixtures
may ship as production behavior. Do not mark a task complete if its required
checks remain blocked; distinguish implemented from verified.

R1 is read-only review plus non-mutating validation. Report findings against task
IDs and return fixes to the owning model; do not automatically rewrite code,
commit, push, restart a running service, or open a PR. The user must explicitly
request those actions. Review the entire combined diff, including the Sol-owned
backend, in a fresh review context.

### Copy-paste task instructions

Select the assigned model/reasoning in the client first; task labels do not switch
models automatically. Replace the bracketed ID with one ID from the table.

For Sol implementation:

```text
Read docs/beta-job-status-plan.md and repository instructions. Implement only
task [S1/S2/S3/S4/S5 — choose one]. Read other sections for constraints, but do not
implement them. Verify prerequisites first; stop if missing. Preserve unrelated
changes. Run this task's checks and report the handoff specified in the plan.
Do not commit, push, restart services, or proceed to another task.
```

For Luna implementation:

```text
Read docs/beta-job-status-plan.md and repository instructions. Implement only
task [L1/L2/L3/L4 — choose one]. Verify its prerequisites and use the completed
Sol handoff. Do not implement or change S1–S5 or other L tasks. If the existing
contract cannot support the task, stop and explain instead of changing backend
semantics. Run this task's checks and report the handoff specified in the plan.
Do not commit, push, restart services, or proceed to another task.
```

For the final Sol review:

```text
Perform only R1 from docs/beta-job-status-plan.md. Review the implementation
against all acceptance criteria and run relevant non-mutating validation.
Do not edit files. Report actionable findings with file/line references, owning
task IDs, and any unverified requirements. Do not commit, push, or restart services.
```

## Feasibility and scope

This can be implemented mostly in new files, with small constructor changes in
`server/server.go` and changes confined to the beta renderer and assets.
Keep the existing job structs, output-handler interface, database schema,
execution results, classic dashboard, and terminal protocol unchanged.

The dashboard currently lists retained job output, including completed jobs; it
does not contain an authoritative running-job list. Add per-job metadata in a
separate in-memory store, keyed by the existing job ID. Observe the existing
runner's result and join this metadata to the existing beta job rows.

Deliver:

- Running, Succeeded, Failed, and Unknown labels on individual job rows.
- `+2 ~2 -2` on successful plan rows with a recognized Terraform/OpenTofu summary.
- Accurate zero counts for a recognized no-change plan.
- Explicit import/forget counts when present, for example `import 1 · forget 1`.
- Separate Jobs (`/beta`) and Locks (`/beta/locks`) pages with shared beta chrome.
- Compact job history with counts beside the corresponding output link.
- One bounded, on-demand terminal window on Jobs, reusing the existing terminal.
- Existing filters, grouping, direct job URLs, and total job counts.

The page split and output window are additive beta features: a new handler,
presentation helpers, template partial, and assets, with small changes to beta
navigation, section selection, and filters. Moving sections cannot literally be
new-files-only; these limited integration edits are required. Do not replace the
classic dashboard or redesign the existing terminal to implement this plan.

Keep the initial feature server-rendered. A refresh obtains current status.
Automatic polling, PR-level aggregate status, persisted history, queue status,
and counts on apply rows are separate follow-ups.

## Existing sources to reuse

| Source | What it provides |
| --- | --- |
| `server/events/command/project_context.go` | Existing `JobID`, project identity, and `SuppressJobOutput`. |
| `server/events/command/project_result.go` | `ProjectCommandOutput.Error`, `Failure`, and `PlanSuccess`; the existing failure classification. |
| `server/events/models/models.go` | `PlanSuccess.Stats()` and `NoChanges()`; existing import/add/change/destroy/forget parsing, including aggregation across multiple summary lines. |
| `server/events/project_command_runner.go` | `ProjectOutputWrapper`, which wraps project execution and publishes terminal output and VCS statuses. |
| `server/core/runtime/pre_workflow_hook_runner.go` and `post_workflow_hook_runner.go` | Hook execution returns its final error, including errors reading the custom status file. |
| `server/jobs/project_command_output_handler.go` | Existing job inventory and per-project output cleanup. |
| `server/beta_dashboard.go` and `server/controllers/web_templates/beta_dashboard.go` | Beta route and presentation adapter. |
| `server/controllers/web_templates/templates/project-jobs.html.tmpl` | Existing full-page XTerm viewer, using its own pathname plus `/ws`. |
| `server/static/js/beta-dashboard-filters.js` | Existing metadata filtering; currently assumes Jobs and Locks are rendered together. |

Do not infer success from `OutputBuffer.OperationComplete`, a closed WebSocket,
the presence of a lock, or text such as `Error:` in the terminal. Stream completion
does not distinguish successful and failed commands. Do not use the current
per-project database status to label an older execution of the same project.

## 1. Add an isolated metadata store

Owner: **S1 — GPT-6.1 Sol / Medium**. Prerequisites: none.

New files: `server/jobs/beta_job_status.go` and its tests.

Introduce beta-specific types rather than adding fields to `JobIDInfo`,
`JobInfo`, `ProjectCmdOutputLine`, `OutputBuffer`, or `PullInfoWithJobIDs`.
A snapshot entry needs:

```go
type BetaJobSnapshot struct {
    JobID       string
    Pull        PullInfo
    Operation   string
    Status      BetaJobStatus
    StartedAt   time.Time
    FinishedAt  time.Time
    PlanStats   models.PlanSuccessStats
    HasPlanStats bool
}
```

Use a status enum with Unknown as its zero value. Store no raw output, error
messages, credentials, or mutable result pointers. Store plan counts as values.

Provide small operations to start, finish, mark an interrupted execution unknown,
mark a deferred finalization failure, take a copied snapshot, and remove entries
for an exact `PullInfo`. Starting an observation returns an opaque generation
token. Finish/interruption updates require both the job ID and that token, so an
old callback cannot overwrite a newer observation or recreate a deleted entry.

Use one mutex for this new store. Hold it only for map operations and copying;
never hold it while invoking a runner, output handler, VCS client, or template.
Take a single copied snapshot per dashboard response.

Bound the store: use an internal initial limit of 10,000 entries, injectable in
tests. When full, evict the oldest terminal entry. If every entry is running,
decline the new observation and allow execution to proceed. Missing or evicted
metadata renders Unknown. Do not evict a running entry merely because it is old,
and do not add a background goroutine or public configuration flag for this.

Starting a new observation clears previous counts for that ID. Finishing a
deleted or evicted observation is a no-op. Repeated finish callbacks must not
change a completed result. A deferred finalization failure is the explicit
exception: it may change Succeeded to Failed, never Failed back to Succeeded.

## 2. Observe project execution with a new decorator

Owner: **S2 — GPT-6.1 Sol / Medium**. Prerequisite: S1. Implement with section 3.

New files: `server/beta_job_observer.go` and its tests.

Add a beta-only runner that embeds `*events.ProjectOutputWrapper` and holds the
new store. Override Plan, Apply, PolicyCheck, ApprovePolicies, Version, Import,
and StateRm with thin calls to one shared observation helper. Pass the actual
method's operation to the helper; keep the original context and result intact.

The helper must:

1. Bypass observation when the store is nil, `JobID` is empty, or
   `ctx.SuppressJobOutput` is true. Still call the original runner exactly once.
2. Record Running before invoking the embedded wrapper's corresponding method.
3. Call that method exactly once and return its original result.
4. Record Failed if `Error != nil` or `Failure != ""`; otherwise record Succeeded.
5. Extract counts only for a successful Plan, according to section 3.
6. Use a defer to mark an unfinished observation Unknown on abnormal exit.
   Preserve panic propagation; do not recover or convert a panic into success.

`SuppressVCSStatus` alone must not suppress observation. It is a separate
concern from `SuppressJobOutput`.

Preserve both `events.DeferredPlanStatusPublisher` and
`events.DeferredApplyStatusPublisher`. Add explicit forwarding methods that call
the embedded wrapper's publisher once with unchanged arguments. If the callback
reports `models.FailedCommitStatus`, update only the corresponding observed
jobs to Failed and hide their plan-count display. Match each supplied result to
its context using command, project name, directory, and workspace, then use that
context's JobID. Do not fail unrelated jobs or older runs of the same project.
For other callback statuses, preserve the recorded execution result.

Approved S2 identity constraint: deferred callback correlation relies on the
existing context builder's fresh UUID per job. Distinct executions must have
distinct JobIDs, even for the same repository/project/workspace. The unchanged
publisher interfaces carry original contexts/results but no observation token,
so S2 does not promise to distinguish callbacks for two executions that reuse an
identical JobID/context. Do not add token fields to existing execution models or
change those interfaces to support that unsupported case.

Retain the Start token in beta-only observer bookkeeping keyed by the unique
JobID and matching project/operation identity, and pass that token to S1 for
deferred updates. S1's generation checks remain unchanged. Bound auxiliary
bookkeeping, release completed callback records, and account for workflows that
never publish deferred statuses; do not retain output, errors, or mutable result
pointers. Missing or ambiguous correlation must not update an unrelated job or
fall back to a job-ID-only store update. If bookkeeping capacity prevents safe
tracking, decline observation rather than affect execution or report fabricated
success. Document the implemented retention policy in the S2 handoff.

This matters because successful Terraform execution can be followed by a failure
persisting the command result. A project can briefly show execution success
before that later failure is reported; the next dashboard snapshot must show
Failed. The badge describes this job, not the aggregate PR, its approval state,
or permission to apply.

Add compile-time interface assertions for the runner and both deferred publisher
interfaces. Embedding only `events.ProjectCommandRunner` would hide its optional
publisher interfaces and is insufficient here.

Critical wiring constraint: keep the original `projectOutputWrapper` and its
underlying `DefaultProjectCommandRunner`. The cancellation constructor currently
receives `projectOutputWrapper.ProjectCommandRunner`, and cancellation asserts
that concrete type. Do not insert this decorator inside that field.

The resulting project execution chain is:

```text
InstrumentedProjectCommandRunner
  -> beta observer
    -> existing ProjectOutputWrapper
      -> existing DefaultProjectCommandRunner
```

## 3. Derive counts from the existing final plan result

Owner: **S2 — GPT-6.1 Sol / Medium**, as part of the section 2 observer task.

In the new observation helper, after a successful Plan:

1. If `result.PlanSuccess == nil`, leave `HasPlanStats` false.
2. Otherwise compute `stats := result.PlanSuccess.Stats()` once.
3. Set `HasPlanStats` when `stats.Changes` is true or
   `result.PlanSuccess.NoChanges()` is true.
4. Copy the full stats value, including Import and Forget.

The display maps Add to `+`, Change to `~`, and Destroy to `-`. These are resource
action counts, not changed files, lines, or a net resource total. Replacement
actions can contribute to both Add and Destroy.

The parser returning all zeros does not by itself prove a no-change plan.
Unrecognized/custom output remains count-unavailable, even if execution succeeded.
Use the existing parser without introducing another regex or parsing streamed
lines on each dashboard request. Apply, policy, version, import, state, and hook
rows do not inherit counts from the last plan.

## 4. Observe workflow hooks and connect cleanup

Owner: **S3 — GPT-6.1 Sol / Medium**. Prerequisite: S1.

New files: `server/beta_workflow_hook_observer.go`,
`server/beta_job_cleanup.go`, and their tests.

Pre- and post-workflow runtime runners have the same `Run` signature. Add a
decorator around each injected runtime runner that records by `ctx.HookID`.
Record Running before Run and Succeeded/Failed from its returned error afterward.
Return all three original return values unchanged. Honor `SuppressJobOutput`,
empty IDs, nil store, and abnormal exits as for project execution. Hooks never
have plan counts. Observe the whole Run method so a custom-status-file read
error is included in the outcome.

Build hook identity exactly as `SendWorkflowHook` does: repository name/full name
and request number, with empty project, directory, and workspace. Do not populate
those fields from other hook context fields or invent a default workspace; that
would prevent the snapshot from matching the existing job inventory. For project
jobs, mirror the identity built by the existing output handler's `Send` method.

Add a cleaner implementing the existing `events.ResourceCleaner` interface.
It delegates `CleanUp(pullInfo)` to the original output handler exactly once and
removes matching metadata. Use exact project identity; do not clear other
repositories, requests, workspaces, or projects. Wire this only into
`PullClosedExecutor.LogStreamResourceCleaner`.

The existing cleanup path only visits projects recorded in pull status. It does
not guarantee cleanup for every hook or early failure. The store's size bound
covers that limitation without refactoring existing cleanup behavior.

## 5. Make the small constructor changes

Owner: **S4 — GPT-6.1 Sol / Medium**. Prerequisites: S2 and S3.

Edit only the relevant construction sites in `server/server.go`:

- Add an optional `BetaJobStatuses *jobs.BetaJobStatusStore` field to Server.
- Create one store for the server when the async output handler is selected.
  Leave it nil for `NoopProjectOutputHandler` remote-execution mode.
- Pass the beta project observer to `NewInstrumentedProjectCommandRunner`.
- Pass that observer to `NewVersionCommandRunner` too, because version currently
  bypasses the instrumented runner.
- Wrap the injected pre- and post-workflow runtime runners.
- Supply the new cleaner to `LogStreamResourceCleaner`.
- Assign the store to the Server literal.

All observers and the renderer must tolerate nil. Existing tests constructing a
Server without the new field must continue to work. Preserve the original output
handler for Terraform, shell commands, WebSockets, and the classic dashboard.
Do not change constructor signatures or generate new mocks for existing interfaces.

## 6. Join snapshots into the beta view

Owner: **S5 — GPT-6.1 Sol / Medium**. Prerequisite: S4. Publish the data contract
in the handoff before Luna begins presentation work.

New file: `server/controllers/web_templates/beta_job_details.go`, plus tests.
Small edits: `server/beta_dashboard.go` and the existing beta grouping/writer file.

Keep `IndexData`, `GroupBetaDashboard(IndexData)`, and
`BetaProject.Jobs []jobs.JobIDInfo` compatible. Add a beta-only details map keyed by
job ID to `BetaDashboardData`. A new template-writer factory accepts a copied
snapshot and enriches the result of the existing grouping function.

Keep `BetaDashboardTemplate` as a valid zero-snapshot Jobs-page default for
existing callers. Add a beta-only page selector to the writer factory; it must
not require changing `IndexData` or the shared `TemplateWriter` interface.
`Server.BetaDashboard` passes the per-response writer to the existing `renderIndex`.
Do not duplicate `renderIndex`, change `preparePullToJobMappings`, or put status
reads in the classic handler. Keep rendering state on the writer instance, not
in a package-global mutable variable.

Join by JobID, then verify repository, request, project, directory, workspace,
and operation match before exposing the details. A missing or mismatched entry
renders Unknown. Preserve the existing job inventory, relative sorting, URLs,
counts, filter attributes, and handling of lock-only entries. Section 9 partitions
the ordered jobs for presentation only; it does not discard older executions.

Inventory limitation: a job appears only after the existing output handler has
registered it. Thus a silent operation, queued command, or failure before project
execution may not have a row. Hooks currently publish output after execution,
so they may first appear already completed. Do not synthesize output messages,
invent log URLs, or add store-only jobs to make these appear early in this change.

## 7. Add compact beta presentation

Owner: **L1 — GPT-6 Luna / High**. Prerequisite: S5. Consume the established
snapshot view; do not modify how results or counts are computed.

Make small presentation edits to `beta-dashboard.html.tmpl` and
`beta-dashboard.css`:

- Put a short text status next to each existing output link.
- For a successful plan with known counts, show `+N ~N -N` alongside it.
- Show nonzero import/forget counts with words so those actions are not hidden.
- For known no-change plans, display `+0 ~0 -0` with an accessible no-change label.
- For unavailable counts, omit the counts; Unknown must not look successful.
- Give symbols accessible meanings, such as "2 to add, 2 to change, 2 to destroy".
- Use text as well as color and let details wrap on narrow screens.
- Keep status-to-CSS-class mapping limited to known enum values.

Reclaim results viewport space with these small beta-only template deletions:

- Remove the search-help paragraph beginning "Partial names and abbreviations
  work", including its user-metadata explanation. Keep the Search label and
  `Search repos, workspaces, or users` placeholder. Remove the input's
  `aria-describedby="search-help"` reference when removing that element.
- Keep the live results count as the only explanatory line beneath the filters.
  Until the page split, retain `Showing 1 of 2 jobs and 1 of 2 locks`; after the
  split, show only the active page's count, as specified in section 8.
- Remove the visible Jobs section header and "Grouped by repository and
  workspace" description. Keep a visually hidden Jobs heading with the existing
  ID so the section's accessible name and navigation anchors remain valid.
  The sidebar continues to identify Jobs; do not add another visible title.
- Remove the entire "Tracked job output only. Lock-only projects are listed in
  Locks. Operation results are not available in this beta." note. Do not replace
  it with another dashboard paragraph or leave an empty header wrapper/gap.

Put refresh behavior, per-job scope, unknown counts, memory retention, user
metadata limitations, and unsupported inventory in `docs/beta-dashboard.md`
instead of adding instructional text above the results. Keep this cleanup
limited to beta presentation; no search behavior or shared data-model changes.
Keep the existing matching behavior, with the page-scoping changes in section 8;
status filtering and a new polling endpoint are out of scope. Refresh changed
beta asset revisions for the existing cache setup.

## 8. Give Jobs and Locks separate server-rendered pages

Owner: **L2 — GPT-6 Luna / High**. Prerequisite: L1. Use S5's page selector;
the new route is the only `server.go` change authorized by this task.

New file: `server/beta_locks.go`, plus route tests. Add one GET registration in
`SetupRoutes` for `/beta/locks`, under the same middleware as `/beta`.

- `/beta` remains the Jobs landing page; do not introduce another Jobs alias.
- `/beta/locks` renders the existing compact lock rows and their lock-detail and
  PR/MR links. It does not render job history or a terminal window.
- Both handlers reuse `renderIndex` with the beta writer factory and a typed page
  selector. Keep fetching existing data, including locks used to enrich job
  metadata and totals; this is not a storage-query refactor.
- Add conditional rendering around the existing Jobs and Locks sections. Render
  only the requested section, not both sections with CSS hiding one. Share the
  existing sidebar, filters, build information, and classic-dashboard link.
- Sidebar links become base-path-aware page URLs. Set `aria-current="page"`
  server-side and add its CSS state. Update the page title and accessible heading.
  Limit the existing hash-navigation script to Apply controls so it cannot erase
  the server-selected page state or intercept Jobs/Locks navigation.
- Keep Apply controls on Jobs, with a sidebar link to `/beta#apply-controls`
  from Locks. Preserve the existing enabled/disabled checks, confirmation,
  endpoint calls, and scroll-to-button behavior. Do not move the apply API.

Make a small, necessary edit to `beta-dashboard-filters.js`: scope its rows,
dropdown values, empty states, and result summary to the active page. Guard
selectors for absent sections. Update only that page's filtered sidebar count;
leave the other page's server-rendered total intact. The existing unguarded
`.lock-list` and empty-state lookups would otherwise throw on a Jobs-only page.
Keep the fuzzy matcher unchanged. Filters may reset on page navigation in this
first version; no cross-page filter persistence is required.

Existing `/lock?id=...`, `/jobs/{job-id}`, `/jobs/{job-id}/ws`, and classic routes
remain unchanged. Document `/beta/locks` as the replacement for the old
`/beta#locks` bookmark; a URL fragment cannot be redirected by the server.

## 9. Keep execution history compact and links unambiguous

Owner: **L3 — GPT-6 Luna / High**. Prerequisite: L2. Partition beta presentation
only; preserve the complete job inventory and status semantics.

New file: `server/controllers/web_templates/beta_job_history.go`, plus tests.
Add beta-only presentation fields for primary and earlier job lists; retain
`BetaProject.Jobs` as the unchanged complete inventory.

Partition each project's already-sorted jobs without mutating that inventory:

1. Keep the first plan and first apply in the existing order visible, plus every
   job whose matched snapshot says Running. Deduplicate by job ID.
2. If that selection is empty, keep the first job visible, so hooks and other
   operations still have a visible entry when metadata is missing.
3. Put the remainder under a native `details` control, `Earlier output (N)`.
   Preserve the existing relative ordering in both lists. Determine operation
   from the matched metadata or the existing structured `JobStep`, never from
   free-form descriptions or terminal text.

The existing job timestamp is an output-update time, not necessarily execution
start. Keep that meaning; do not promise chronological execution history or
change the core timestamp model. Empty inventories produce no disclosure.

Each execution appears exactly once in the DOM, retains its `.job-link` marker,
and displays operation, status, available counts, timestamp, and an `Output` link
to that exact job ID. Collapsing earlier output must not reduce job totals or
remove entries from metadata filtering. Style nested history summaries narrowly:
the existing `.repository summary` rules also match descendant summaries.

For example, a successful plan row reads `Plan · Succeeded · +2 ~2 -2 · Output`.
Its link opens that plan, never the latest job for the project. An apply row has
its own output link and status. Do not infer that an apply consumed the displayed
plan: the current dashboard inventory does not establish that relationship.
Earlier runs retain their own counts and links when expanded.

## L4 implementation handoff

Implemented by GPT-6 Luna / High with a new shared
`templates/beta-job-output.html.tmpl` partial and new
`server/static/js/beta-job-output.js` and
`server/static/css/beta-job-output.css` assets. Narrow edits to the Jobs-only
beta template include the partial and assets only on `/beta`; Locks does not
load them. The existing beta stylesheet cache revision advanced to v7.

Each existing Output anchor remains a working full-page `/jobs/{id}` link and
provides escaped exact-row metadata as data attributes. `BetaJobHistoryRow`
populates its repository, workspace, and project fields from the matched
request/project identity. Ordinary unmodified activation validates that its
generated URL is a same-origin job-view route, opens the shared native dialog,
and creates one iframe from that URL. Modified clicks and no-JavaScript behavior
continue through the original anchor. The header uses text content for
repository, workspace, project, operation, time, status and counts; direct
access is always available from the dialog.

The iframe is created only after the dialog opens, removed before any subsequent
selection, and removed on close or pagehide. Native dialog focus handling and
Escape cancellation are retained. A guarded same-origin keydown bridge handles
Escape while focus is inside the iframe; close restores focus to the invoking
Output link. The load status explicitly does not claim WebSocket connection or
job success. A persistent note describes expired/unavailable output and keeps
the full-page route available. No terminal scripts/protocol, security headers,
or CSP behavior were changed.

Per task instruction, no tests or builds were added or run. Browser QA was not
performed because the task constraints prohibit launching a new server/binary;
the live user server was not accessed. Resize/refit, authentication, framing
denial, expired streams, iframe Escape behavior, and connection disposal remain
unverified for R1. The Escape bridge catches cross-origin access failures; in
that case the Close button and full-page link remain available. No
application-level CSP or X-Frame-Options header was found in the source, but
deployment-layer framing policy remains unknown. Keep direct full-page Output
access if iframe reuse is denied; do not weaken security headers.

## 10. Open one bounded output window from Jobs

Owner: **L4 — GPT-6 Luna / High**. Prerequisite: L3. Keep the existing terminal
implementation unchanged and report reuse blockers rather than rewriting it.

New files: `templates/beta-job-output.html.tmpl` under `web_templates`,
`server/static/js/beta-job-output.js`, and
`server/static/css/beta-job-output.css`. Include the new partial and assets only
on Jobs. Existing template and static embedding already cover these paths.

Use one shared native modal `dialog`, not an embedded terminal for every row.
This keeps history compact without permanently consuming results height. Start
with a desktop window capped at about 1100px wide and 640px high, bounded by the
viewport with 16px margins; on phones use the available viewport within those
margins. Give the terminal the remaining space beneath a compact header. Scope
styles to this dialog so the existing Apply confirmation is unaffected, and
reuse beta colors, spacing, focus styles, and typography.

The header identifies the selected repository, workspace, project, operation,
and timestamp, and repeats that execution's snapshot status/counts. Provide an
always-visible Close button and an `Open full page` link. Label the iframe and
dialog accessibly. Escape, focus containment, and returning focus to the invoking
link must work, including when focus is inside the terminal iframe. Preserve
the Jobs page's scroll position, filters, and expanded history on close.

Enhance the existing real `/jobs/{job-id}` anchors only for ordinary activation.
Modified clicks and JavaScript-disabled browsers retain normal link behavior.
Set the iframe source from that server-generated anchor URL, including the base
path; accept only the same-origin job-view route. Do not accept arbitrary query
URLs, build HTML from metadata, or assemble WebSocket URLs in the parent page.
Use text nodes for the header. Unsupported dialog behavior falls back to the link.

Create the iframe after opening the dialog, so the existing terminal can measure
a visible viewport. Its source must remain `/jobs/{job-id}`: that page appends
`/ws` to its own pathname. Its existing fixed-position terminal fills the iframe
rather than the entire dashboard. Reuse existing XTerm, replay, search, and live
streaming without modifying their scripts or protocol. Remove the previous frame
before selecting another job; remove it on close/navigation. There must be no
background terminal connections for collapsed history or closed windows.

Show a loading message while the frame navigates, and keep the full-page link
available throughout. Frame load does not prove WebSocket connection or job
success. Explain that output may have expired and offer reopening/full-page
viewing for a blank or unavailable stream; do not fabricate an empty successful
result or add an output-probing API. Parent badges remain dashboard snapshots,
with refresh required for updated results. The terminal's existing `Done` text
describes stream completion, not Succeeded.

Verify frame resize/refit, authentication, expired output, and connection disposal
before accepting iframe reuse. The existing terminal unload handler references
`websocket` although its variable is named `socket`; do not assume that handler
works or couple the new module to terminal globals. Verify disposal through the
iframe lifecycle. If a deployment blocks framing, keep full-page access; do not
weaken security headers. If reuse cannot work within these constraints, retain
the direct links and report the viewer as blocked instead of expanding this work
into a terminal rewrite.

## Verification and acceptance criteria

Ownership: each S/L task implements and runs its relevant checks below.
**R1 — GPT-6.1 Sol / Medium** independently reviews the combined implementation
and verification evidence after all tasks. R1 reports issues without editing;
fixes return to the task owner. No model may omit checks because another owns
the final review.

Use tests of observable behavior, especially the boundaries below:

- A blocking fake runner is Running while blocked, then succeeds or fails after
  release. The wrapped method is called once and returns the same result.
- Both Error and Failure classify as Failed. Output containing "Error:" with a
  successful returned result does not falsely fail the job.
- Panics still propagate and do not leave Running forever or report success.
- Suppressed output, empty IDs, nil store, and Noop mode create no metadata.
- A new run cannot inherit old counts; stale completion and cleanup races cannot
  overwrite another observation or resurrect a deleted one.
- S1 continues testing same-ID stale-generation rejection. S2 deferred-callback
  tests use distinct IDs for distinct executions, including delayed callbacks
  for an older run of the same project. Reusing the exact same JobID/context for
  separate executions is outside S2's approved correlation contract.
- Store capacity, copied snapshots, exact cleanup identity, and concurrent
  observation/read/cleanup behavior are tested without sleeps.
- Both deferred publishers remain callable, delegate once, and a finalization
  failure changes only the matching observed job to Failed.
- Cancellation still receives the original concrete default runner.
- Hook success, hook command failure, and status-file read failure are recorded.
- Counts cover ordinary changes, recognized no changes, unknown output,
  multiple summaries, replacements, imports, and forget operations. Apply and
  failed rows do not show plan counts.
- Missing metadata renders Unknown; classic rendering remains unchanged; URLs
  respect the Atlantis base path; unsafe metadata remains escaped.
- Existing job filters/counts work with badges, and long workspace/path/status
  content fits at desktop and mobile widths. No output stream protocol changes.
- The search placeholder and live results count remain; the search-help text,
  visible duplicate Jobs heading, grouping description, and data note are gone.
  Accessible headings remain, with no dangling ARIA references or empty spacing.
- Direct GETs and sidebar navigation select genuinely separate Jobs/Locks pages,
  with correct titles/current-page state and no inactive-section DOM. Both work
  under a nonempty base path and existing authentication; classic is unchanged.
- Page-specific filters handle empty pages without null-selector errors, preserve
  the other page's total, and keep Apply controls reachable from either page.
- History partitioning preserves every job exactly once, including hooks,
  unknown metadata, all running entries, ties, and multiple plan/apply runs.
  Collapsing history does not alter counts or exact-job output destinations.
- Browser checks cover selecting different executions, opening earlier output,
  Escape/focus return from inside the iframe, no-JavaScript and modified clicks,
  and retained filters/scroll. Use desktop and mobile screenshot evidence before
  claiming the layout fits; no clipped headers or inaccessible Close button.
- Only one terminal is mounted at a time. Closing/switching releases the previous
  WebSocket subscription, resizing refits XTerm, and completed output replays.
  Check missing/expired output and denied framing without misreporting success.

Run targeted Go tests for `./server/jobs`, `./server/events`,
`./server/controllers/web_templates`, and `./server`; skip only the documented
pre-existing `TestNewServer_GitHubUser` failure if necessary. Run race tests for
the new store and observers, `make check-fmt`, and a build. Use a temporary local
fixture for browser checks; keep mock/testdrive additions out of this feature.

Implement in reviewable steps: store and tests; observers/cleanup and tests;
constructor wiring; beta adapter and badges; page split and filter guards;
compact history; bounded output viewer; documentation and verification. Follow
the model-assignment order above. The page split/history/viewer can be reviewed
as a separate UI-only follow-up, but the assigned execution sequence assumes S5's
data contract already exists. A standalone UI-first sequence needs an explicitly
revised task assignment, not invented backend stubs.
The existing execution, command-result, job-handler, database, and terminal files
should require no modifications. Most production changes belong in new files;
existing-file replacements should be confined to construction, one route
registration, and beta rendering/navigation/filter integration. Update
`docs/beta-dashboard.md` with the new pages, output window/fallback, history
disclosure, and the distinction between job results and locks.

Before an upstream PR, agree on the added behavior in an accepted issue and follow
the repository's AI disclosure and DCO rules. This plan proposes no change to
existing execution/locking contracts; if implementation requires one, stop and
revisit the repository's ADR process rather than expanding this change silently.
