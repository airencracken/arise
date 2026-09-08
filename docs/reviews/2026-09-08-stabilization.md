# Stabilization and documentation review, 2026-09-08

Baseline: Arise 0.0.32, source `cc2df3f7a4b87534acd29c564043121b930de8f5`.
Work was isolated on `fix/stabilization-audit` after pulling master.
This is a bounded audit and regression batch, not a full Portage-parity claim.

## Confirmed failures and corrections

| Area | Reproduced failure | Correction and regression |
|---|---|---|
| Executor graph | Serial execution ignored prerequisites. Parallel execution could run an independent action before discovering a later cycle. Both could replace resume state before rejecting an invalid graph. | Validate the complete DAG before resume publication or runner execution; share the scheduler across job counts. `TestAuditRejectsInvalidGraphBeforeResumeOrRunner`. |
| Commit proof | Serial fake/failed runners could return success without a commit or notify twice. Ignored callback failures could release dependents. | Require a successful completion notification and retain callback errors independently of runner return values. `TestAuditSerialRequiresExactlyOneCommitProof`, `TestAuditIgnoredCommitCallbackFailureStillStopsExecution`. |
| Build-only validation | Archive production was modeled as installation, requiring runtime tools and treating another archive as an installed build tool or replacement library. | Preserve installed dependency roots; validate source build requirements and metadata separately. `TestBuildOnlyValidatesInstalledBuildToolsWithoutChangingRuntimeState`, `TestBuildOnlyCannotBreakRetainedRuntimeOrBypassAuthority`. |
| Build-only execution | Successful source archive creation failed because no package transaction was committed. Successful execution would also add world selections and false merge timing. | Complete archive jobs after successful production without mutation callbacks, world selection, or merge timing. Reject selected binary install actions in build-only mode. `TestAuditBuildOnlyCompletesWithoutPackageMutation`, `TestBuildOnlyDoesNotSelectWorldOrWriteMergeTiming`. |
| Resume | Save could publish duplicate atoms that its own reader rejects; completing an unknown atom could silently succeed. | Reject duplicates before publication and unknown completion before rewriting. `TestResumeDuplicateSaveAndUnknownCompletionAreAtomic`. |
| Cancellation | A cancelled context passed through an unthrottled load wait; cancelled execution could replace resume state. | Honor cancellation at executor entry and at load admission, including a disabled load threshold. `TestAuditCanceledExecutionPreservesResumeAndSkipsPreflight`, `TestCanceledLoadWaitWithoutThrottleDoesNotAdmitWork`. |
| Binary index | Encode/publication accepted invalid field delimiters, injected records, escaping paths, and invalid sizes. | Validate fields and reparse the encoded document before touching the published index. `TestIndexPublicationRejectsInjectedRecordsAtomically`. |
| Binary index permissions | Atomic publication used a temporary file's 0600 mode, blocking ordinary binhost readers. | Publish the conventional 0644 index. `TestPublishedIndexIsReadableByBinhost`. |
| Binary instance choice | BUILD_TIME was compared lexically, selecting 9 over 10. | Validate numeric timestamps and compare their values. `TestNewestInstanceUsesNumericBuildTime`. |

Additional regression checks preserve rejection of invalid REQUIRED_USE during
build-only rebuilding, detect ignored build-only mutation notifications, retain
metadata authority, exercise real source/binary transactions, and keep
pre-existing runtime defects separate from invalid new builds.

## Real-worker evidence

`TestBuildOnlyRealWorkersPublishWithoutInstalling` executed, without a skip,
using Portage sandbox and real Bash phase workers against synthetic repositories
and disposable roots. Both serial and parallel cases produced two indexed GPKG
archives and completed their resume records without installing payloads or VDB
entries. The combined run took 4.19 seconds (2.78 serial, 1.41 parallel).

The full suite also retains real-worker install, binary installation,
modified-file removal, journal rollback, process-death, schema, API, route,
property, and adversarial tests. Capability-dependent tests are not a substitute
for a fresh-stage3 gate, and this review does not claim all optional tests ran.

## Documentation and punchlist

The active punchlist was reduced from 2,797 lines to an open-only backlog,
organized into Now, Next, and Later. Completed entries were removed; no new
archive copy was created. Remaining requirements from the obsolete consolidation
plan were retained in the backlog, then that duplicate plan was deleted.

The documentation pass checked the tracked Markdown corpus and reviewed active
entry points, operational guides, testing instructions, planning/evidence
indexes, the compatibility matrix, and installed manuals against code.
Corrections include:

- README and both stage3 guides invoked the removed `update` command. Examples
  now use `--update @world`; tests no longer require the obsolete spelling.
- Testing guides incorrectly promised a suite without subprocesses or sockets
  and claimed disposable-root mutation tests were absent from ordinary tests.
  They now distinguish actual capability needs and separate fresh-system gates.
- Documentation/evidence indexes presented July milestones as current status.
  Historical records are now labeled by scope and date.
- Snapshot-provider policy options were insufficiently distinguished from
  available CLI options, and recovery capture was described only as future work.
- Two relative links in the archived independent-validator plan were broken.
- The man page had a redundant paragraph macro flagged by mandoc.
- Build-only requirements and source/binary limitations are stated in both
  manuals and the compatibility matrix.

The local documentation checker now validates Markdown file targets. Before
this report was added it checked 104 Markdown files and 128 local links.
External URLs were not network-validated; historical experiments and every
upstream compatibility claim were not independently rerun. Dated evidence was
preserved rather than rewritten to imply current results.

## Verification

The following passed on the final production changes:

- `go test ./... -count=1 -timeout 120s`.
- `go vet ./...`.
- `go test -race ./internal/... ./cmd/arise -count=1 -timeout 300s`.
- `make bench-quick` (47.06 seconds). This is a regression smoke lane, not a new
  same-snapshot Portage performance claim.
- `support/check-docs.sh`: local links, Bash syntax, Info compilation, man-page
  lint, and diff whitespace.
- Focused documentation route, stage3, handbook, version, and punchlist contracts.
- Existing complexity ratchets: average 7.86, 18 functions above 50, maximum
  at most 215, compared with baseline average 7.87 and 19 above 50.

Five-second fuzz runs with two workers passed:

| Target | Inputs executed |
|---|---:|
| Packages index parse/encode round trip | 62,171 |
| Version comparison invariants | 284,115 |
| Dependency-expression round trip | 284,280 |

Eight actual source mutations were killed by regression assertions: bypassed
DAG preflight, missing duplicate-commit rejection, disabled build-only
validation, missing index field validation, private index permissions,
reversed timestamp ordering, duplicate resume publication, and cancellation
bypass. Mutated files were restored before subsequent checks.

## Limits and next work

No host packages, host configuration, or overlay release were changed. The
same-snapshot Portage matrix, exhaustive option interactions, full recovery-set
promotion, and fresh-stage3 gates remain open in the punchlist. Passing this
batch does not close them or certify arbitrary lifecycle writes or snapshot
backends. Keep these fixes together for the next explicitly requested release.
