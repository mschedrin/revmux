# OpenCode V2 support on stable revmux

## Overview

Add OpenCode as a third review executor in `mschedrin/revmux`, based on upstream **v0.2.7**.
Allow each reviewer, synthesis stage, and verification stage to select an OpenCode model and reasoning variant.
Provide an OpenCode caller skill that works from the full TUI or Mini.
Keep the existing Claude and Codex behavior.

This plan records the implementation and its validation.
The user selected the `opencode run` approach and **code first, then tests** for each implementation task.
The requested adviser and reviewer is the `ask` agent using **`caigw/kimi-k3#high`**.

## Context

### Repository and release

- Fork created: <https://github.com/mschedrin/revmux>.
- Latest stable release checked on November 15, 2026: `v0.2.7`, published October 18.
- Exact base: `3260c28ca3d202783a16d7ff4925a069119c428e`.
- Worktree: `/Users/mschedrin/dev/revmux-opencode`.
- Work branch: `feat/opencode-v2`, created at that tag.
- The original checkout remains at `/Users/mschedrin/dev/revmux`.
- The GitHub fork initially copied upstream `master`; the maintained OpenCode branch will be based on the stable tag and become the fork default after implementation passes its checks.
- Do not force-reset the copied `master` branch.

At this tag, the main package is **`app/`**, not `app/revmux/`.
Build the stable tree with `./app`.
Later upstream changes, including the synthesis singleton change and main-package relocation, are outside this port.
Document the stable base clearly so users do not assume the fork includes unreleased upstream changes.

### Reference implementation

Use [hackmajoris/revmux PR #1](https://github.com/hackmajoris/revmux/pull/1) as reference.
Its implementation commit is `319c81ac15e83da999924c7f14f8637f74bd62f6`.
Retain its attribution for adapted work.
Do not merge its whole branch: that would also import older scripts, documentation, and release settings.

The old executor uses `--variant`, assumes requested and actual models are identical, and ignores stderr.
Those choices must not carry into the V2 port.
Its event decoder is a starting point, not proof of the V2 stream contract.

### Verified OpenCode interface

Checked against the installed **OpenCode v2.3.0**, `run --help`, `mini --help`, and official documentation:

- `opencode run --format json` is the automation interface.
- `--model provider/model#variant` selects both the model and reasoning variant.
- There is no separate `--variant` flag in the installed CLI.
- Variant IDs are model-specific and can be custom names such as `deep`.
- `--thinking` controls the display of reasoning; it does not select reasoning effort.
- `--archived` creates a session outside the normal session list.
- `--auto` approves permissions unless an explicit deny applies.
- `--standalone` uses a private server rather than the shared background service.
- Mini is interactive and has no JSON-output option.

Sources:

- <https://opencode.ai/docs/cli>
- <https://opencode.ai/docs/models>
- <https://opencode.ai/docs/skills>

The JSON stream, stdin behavior, private-server teardown, and reasoning-event cadence were measured
in Task 1. The evidence is recorded below and in `.claude/rules/executor.md`.

### Existing integration points

- `app/executor/proc.go`: process launch, stdin, stdout tee, stderr drain, timers, environment.
- `app/executor/executor.go`: per-run request and result.
- `app/executor/codex.go`: prompt-based JSON contract and tolerant JSON extraction.
- `app/prompt/runner.go`, `roster.go`: model parsing, inheritance, executor vocabulary.
- `app/main.go`: `runnerFactory`.
- `app/pipeline/pipeline.go`: `archivedPrompt`, currently assumes every non-Codex runner is Claude.
- `app/pipeline/find.go`: raw-output filenames; OpenCode should use `.jsonl`.
- `app/introspect.go`, `app/artifacts.go`: resolved selection and archive reporting.
- `.claude-plugin/skills/revmux/` and `plugins/codex/skills/revmux/`: existing self-contained caller skills.
- `Makefile` and `.github/scripts/`: skill synchronization and shell tests.

No new Go dependency is expected.

## Design decisions

### Model and reasoning syntax

Use native OpenCode references in revmux profiles:

```yaml
model: opencode
```

The bare executor uses OpenCode's configured default. This was the profile's initial selection.
After the port was complete, the user requested `opencode/openai/gpt-6.1-sol#high` as the
shipped `opencode-only` selection for all reviewers and both stages. The profile and its tests
now require that pin; bare `opencode` remains supported in custom profiles.
For a pinned selection, the grammar is:

```text
opencode/<provider>/<model>[#<variant>]
```

Provider/model names in documentation examples must be identified as examples.
Use the user's current model catalog for live tests.
Do not pin the shipped profile to the old fork's subscription or model.

- Keep `#variant` inside `Runner.Model` and pass the complete reference unchanged to `--model`.
- Keep OpenCode `Effort` empty. Record its variant in the existing model fields.
- Do not add another variant field throughout the pipeline or report schema.
- Parse OpenCode separately before the legacy last-colon effort split.
- Preserve colons and additional slashes in OpenCode model IDs, including `ollama/gemma3:4b`.
- Validate reference structure: non-empty provider/model, no empty or repeated `#` suffix, no variant without a model.
- Leave model and variant availability checks to OpenCode. Do not maintain a second model catalog.
- OpenCode does not use revmux's `:effort` suffix. After its provider separator, a colon belongs to the model ID.
- Retain the current strict `:effort` parsing for Claude and Codex, including errors for misspelled effort.
- OpenCode must not inherit a Claude/Codex model or effort.
- The parser and inheritance layer own this rule. The executor also rejects a non-empty `Request.Effort` to catch incorrect internal callers.
- An explicit OpenCode model replaces the complete lower-priority selection, including its variant.
- A bare OpenCode selection can inherit a lower-priority OpenCode model reference as existing same-executor inheritance permits.
- Preserve the current three-layer stage precedence and all Claude/Codex-only inheritance behavior.

`revmux config` must preserve the complete model reference and report no legacy effort for OpenCode.
Document that the existing effort vocabulary applies to Claude/Codex, not to OpenCode variants.
No new catalog field is needed for this port.

### Process ownership

The proposed invocation is:

```text
opencode run --format json --auto --archived --standalone --thinking [--model <reference>]
```

Send the prompt through stdin.
Use one fresh session and private server per executor request, including retries.
Use the existing process-group and timeout machinery.
Do not reconnect retries to a previous session.

Always use standalone mode for this first implementation.
The private server uses a separate process group but exits when its client's stdio connection closes.
Live termination and kernel exit-event measurements confirmed this behavior, at the cost of startup time.
If private-server work survives cancellation, stop this part of implementation and revise the plan before proceeding.
Do not silently fall back to shared-server execution.
The next design to evaluate is explicit API interruption of the revmux-owned session, with verified completion before retry.
That fallback needs a revised plan and user approval; an orphaned review is not an acceptable documented limitation.

Use `--thinking`: live capture showed 19 reasoning events over 155 seconds, with a maximum gap of 20.171 seconds and empty stderr.
These are completed reasoning summaries, not token deltas; other models may have longer silent intervals.
Document its display role separately from model reasoning selection, and the existing `--idle-timeout` setting for such models.
Do not add timer-reset heartbeats generated by revmux itself: they would hide a stalled model.

### Output and failures

- Implement an immutable `OpenCode` executor that embeds `proc`.
- Keep all stream, stderr, and error state local to each `Run` call.
- Append the stage's JSON schema to the prompt with one canonical OpenCode output-contract function.
- Use that same function in `archivedPrompt`; the archived prompt must equal the submitted prompt.
- Decode the captured V2 stream rather than assuming the old fork's field shapes.
- Track answer text using the observed part/message identity and completion events.
- Exclude reasoning, tool results, and earlier commentary from final-answer extraction.
- Accept a JSON answer inside a code fence or surrounding prose.
- Reject truncated outer JSON instead of extracting a valid nested object from it.
- Report stream errors, malformed known events, read failures, and missing final answers as failed/degraded work, not a clean review.
- Unknown event types can be ignored for forward compatibility while their raw bytes still feed the watchdog and archive.
- Retain bounded stderr diagnostics for startup errors and non-zero exit status.
- Do not classify ordinary finding text as a provider error or a rate limit.
- Do not release the stagger gate on startup, informational errors, or banners.
- Read token counts from measured usage events without counting duplicate updates twice.
- Set `ActualModel` only from an observed resolved-model field. Leave it empty if the stream does not report one.
- Preserve the full requested reference, including `#variant`, in catalog and run records.

Do not expand this task into stage raw-stream archival.
At v0.2.7, raw-output tees are supplied for finders; retain that boundary.

### Credentials and caller integration

OpenCode uses its existing configuration and authentication.
Keep `ANTHROPIC_API_KEY` for OpenCode children: OpenCode users can require it for their configured provider.
Retain the current stripping behavior and escape flag for Claude and Codex.
Make this a narrow executor-specific change in `childEnv`, with regression tests.
Continue stripping `CLAUDECODE`.

Add `.opencode/skills/revmux/` as a third self-contained skill tree.
Copy shared scripts and references from the **stable upstream** tree, then maintain three-way synchronization.
OpenCode discovers `SKILL.md` directly; do not copy the Codex-specific `agents/openai.yaml` or add a new plugin manifest.
Adapt `SKILL.md` for OpenCode's current skill paths, tool discovery, questions, and background completion.
Do not hardcode an obsolete `task` tool or assume a background-polling workflow.
The skill works equally from the full TUI and Mini because it invokes revmux through agent tools.
Do not launch Mini as a reviewer or add a session-inspection feature.

Preflight already derives binary names from `revmux config`; add coverage rather than a special OpenCode branch.
Inspect launcher environment forwarding against the required OpenCode settings.
Do not put API keys or inline configuration containing secrets into generated command arguments.
Only change the launcher if a verified configuration requirement needs it; add fake-backend tests for any such change.

## Development approach

- **Regular approach:** implement one task, add its behavior tests, and pass the relevant checks before the next task.
- Every code task includes separate success, error, and regression test items.
- Use mocked processes and the existing injected clock for automated tests.
- Do not call a real model from unit tests or CI.
- Complete contract discovery before writing the decoder.
- Keep changes small and retain existing archive and exit-code contracts.
- Mark completed checkboxes immediately and update this plan when the scope changes.
- Add newly discovered tasks explicitly and record blockers.

## Implementation steps

### Task 1: Verify and record the OpenCode V2 process contract

**Files:**
- Create: `app/executor/testdata/opencode-v2-*.jsonl`
- Create: `app/executor/testdata/opencode-v2-*.stderr.txt` where needed.
- Modify: `.claude/rules/executor.md`
- Modify: this plan, to replace open checks with measured results.

- [x] Run the existing `make test`, `make lint`, and `make check-plugins` on the unchanged stable base. All passed after installing missing Go, golangci-lint, and shellcheck. Baseline coverage: 93.7%.
- [x] Capture a small successful stdin-driven JSON run with the proposed flags, in an isolated temporary working directory.
- [x] Capture a run with a read-only tool call, model output before/after the tool, usage, and a final JSON answer; record the tested version and model reference.
- [x] Check explicit variant selection using a model/variant from the actual catalog; compare JSON mode with and without `--thinking`. Native `#low`/`#high` selections succeeded; `--thinking` supplies reasoning records on a reasoning task.
- [x] Capture invalid model/variant, startup diagnostics, and cancellation behavior. Use derived fixture mutations for other failures.
- [x] Verify private-server process ownership and that cancellation stops its work without stopping the user's shared server. SIGTERM stopped the client and its separately grouped `serve --stdio` descendant. After SIGKILL, kernel exit events confirmed client exit in 0.002 seconds and server exit in 0.355 seconds. The shared service stayed available.
- [x] Measure whether long reasoning and answer generation produce output often enough for the default idle timeout. The captured 155-second reasoning run supplied events at intervals of at most 20.171 seconds with `--thinking`; use that flag and document model-dependent silent intervals.
- [x] Remove credentials and unrelated local context from fixtures, validate every fixture record, and document exactly which records are captured versus derived. The seven-line clean fixture is captured verbatim from an isolated directory and passes the JSON/path checks.

### Task 2: Add native OpenCode model references and inheritance

**Files:**
- Modify: `app/prompt/runner.go`, `app/prompt/roster.go`
- Modify: `app/prompt/runner_test.go`, `app/prompt/roster_test.go`
- Modify: `app/introspect_test.go`

- [x] Add `opencode` to the executor vocabulary and implement the separate native-reference parse path.
- [x] Preserve custom `#variant` names, colon-tagged model IDs, and nested model paths without applying the legacy effort parser.
- [x] Implement the inheritance rules above, including bare defaults and explicit model replacement.
- [x] Test valid references, default selection, per-agent overrides, three-layer stage overrides, and mixed executors.
- [x] Test malformed references, empty variant/model/provider, accidental legacy syntax, and unchanged Claude/Codex error handling.
- [x] Run the prompt and catalog tests. `go test ./app/prompt ./app` passed.

### Task 3: Add the supervised OpenCode executor

**Files:**
- Create: `app/executor/opencode.go`, `app/executor/opencode_test.go`
- Modify: `app/executor/proc.go`, `app/executor/proc_test.go` for the narrow environment policy.
- Modify: `app/executor/testdata/opencode-v2-*` as needed.

- [x] Build the verified invocation and send the prompt on stdin; reject a non-empty OpenCode `Request.Effort` rather than silently ignoring it.
- [x] Implement the captured event decoder, final-answer extraction, activity reporting, observed model reporting, and usage accounting.
- [x] Keep JSON extraction local to the OpenCode executor, using the existing truncation guard as reference; do not refactor Codex's extractor for this port.
- [x] Implement bounded diagnostics and failed-run handling, including typed errors that occur before an otherwise successful process exit.
- [x] Preserve OpenCode provider credentials without changing the existing Claude/Codex environment behavior.
- [x] Test successful output, tool/reasoning interleaving, fenced answers, repeated usage events, and raw-tee fidelity.
- [x] Test malformed/truncated output, valid nested JSON in an incomplete answer, error events, stderr-only failures, missing output, nil sinks, and concurrent executor reuse.
- [x] Test parent cancellation, hard timeout, idle timeout, raw-byte liveness, and child cleanup with fake processes and the injected clock.
- [x] Run all executor tests with the race detector, including the existing Claude/Codex environment regressions. Passed; Kimi review found no blockers and two additional completion cases were added.

### Task 4: Wire OpenCode through the review and archive

**Files:**
- Modify: `app/main.go`, `app/main_test.go`
- Modify: `app/pipeline/pipeline.go`, `app/pipeline/artifacts_test.go`
- Modify: `app/pipeline/find.go`, `app/pipeline/synthesize.go`, `app/pipeline/verify.go` for contract-selection error propagation.
- Modify: `app/pipeline/find_test.go`, `app/pipeline/pipeline_test.go`
- Modify: `app/pipeline/synthesize_test.go`, `app/pipeline/verify_test.go`
- Modify: `app/artifacts_test.go`, `app/introspect_test.go`

- [x] Make `runnerFactory` select Claude, Codex, and OpenCode explicitly. For an unknown executor, return a runner that fails without launching a process; preserve the existing factory interface.
- [x] Give `archivedPrompt` an exhaustive three-executor switch and an error result for unknown executors. Propagate that error at finder, synthesis, and verifier call sites.
- [x] Verify model references reach all three stage requests unchanged. Assert `.jsonl` finder and retry filenames in tests; the current `rawName` implementation already supplies them.
- [x] Test all-OpenCode and mixed-executor reviews, including independently selected stage models and variants.
- [x] Test that unknown executors fail at factory and archive-contract selection rather than silently using Claude.
- [x] Test byte-for-byte prompt archive fidelity for all three executors against their submitted prompts; update the independent artifact test harness.
- [x] Test source failures, retry isolation, truthful requested/actual model reporting, and stage/source statistics.
- [x] Run pipeline and main-package tests before proceeding. `go test -race ./app/pipeline ./app` passed.

### Task 5: Add the OpenCode-only profile

**Files:**
- Create: `app/prompt/defaults/prompts/profiles/opencode-only.md`
- Modify: `app/prompt/defaults_test.go`, `app/prompt/prompt_test.go`
- Modify: `app/initcmd_test.go`, `app/main_test.go`, `app/introspect_test.go`

- [x] Add the four standard lens groups with authored colors. Initially used `model: opencode`;
  the later user-requested selection is `model: opencode/openai/gpt-6.1-sol#high`.
- [x] Keep the stable code-review severity and reporting blocks consistent with the existing equivalent profiles.
- [x] Update all literal profile inventories from eight to nine.
- [x] Initially test the configured-default profile with empty model/effort. After the requested
  model pin, shipped-profile tests require `openai/gpt-6.1-sol#high` and empty legacy effort.
  Parser tests retain coverage for bare OpenCode defaults and rejection of foreign inheritance.
- [x] Test every stage and `--lenses` on OpenCode, plus a custom profile with different native model references per reviewer and stage.
- [x] Run profile, initialization, and catalog tests. `go test ./app/prompt ./app` passed.

### Task 6: Add the OpenCode caller skill and synchronization checks

**Files:**
- Create: `.opencode/skills/revmux/SKILL.md`, `references/`, `scripts/`
- Modify: `.claude-plugin/skills/revmux/SKILL.md`, `references/`, `scripts/` where shared changes apply.
- Modify: `plugins/codex/skills/revmux/SKILL.md`, `references/`, `scripts/` where shared changes apply.
- Modify: `Makefile`, `.github/scripts/check-sentinel-helpers.sh`, `.github/scripts/test-preflight.sh`
- Create: `.github/scripts/test-launch-revmux.sh` only if launcher behavior changes.
- Modify: `.github/workflows/ci.yml` only where required to run the new checks.

- [x] Add current OpenCode caller instructions, profile selection, configuration checks, and report handling.
- [x] Keep shared references and scripts byte-identical across all three installed payloads; retain self-contained copies as required by this repository.
- [x] Extend `check-plugins` and sentinel validation to the OpenCode tree; retain Codex marketplace validation.
- [x] Test preflight with OpenCode-only, mixed-stage, `--lenses`, and missing-binary catalogs.
- [x] Run preflight tests against all three payloads and verify the new skill's required files and relative references.
- [x] Inspect launcher forwarding. Existing HOME/XDG forwarding supports file configuration. No forwarding behavior changed; direct launch is documented for environment-only settings.
- [x] Run script tests, shellcheck, and `make check-plugins`. All passed; Kimi's integration review found no blockers.

### Task 7: Document and package this fork

**Files:**
- Modify: `README.md`, `CLAUDE.md`, `.claude/rules/prompts.md`, `.claude/rules/executor.md`
- Modify: `site/index.html`, `site/docs.html`, `site/reference.html`, `site/llms.txt`
- Modify: shared skill references and installation hints in all three trees.
- Modify: `.goreleaser.yml`, relevant release workflows, and plugin metadata only where fork publishing needs correction.

- [x] Document the fork URL, exact stable base, OpenCode v2.3.0 tested interface, source build, and skill installation.
- [x] Explain native model/variant syntax, default-model behavior, independent stage selection, and the difference between `#variant` and `--thinking`.
- [x] Describe Mini as a caller interface, standalone process ownership, credential handling, and the limits of prompt-based read-only review.
- [x] Update profile/binary tables, prompt-tree diagrams, grammar, skill-tree counts, and invocation references at every keep-in-sync site.
- [x] Attribute the adapted OpenCode implementation and retain the existing license.
- [x] Remove or disable fork release settings that publish to the upstream Homebrew tap; do not invent a working fork tap or release before one exists.
- [x] Check installation commands against this stable layout and ensure OpenCode installation instructions point to the user's fork.
- [x] Validate modified configuration, skill synchronization, and documentation references. GoReleaser v2.20.2 check passed. All three HTML pages have balanced markup and unique IDs.

### Task 8: Verify acceptance and prepare publication

**Files:**
- Modify: this plan with check results and any remaining limitations.
- Modify: affected tests only if verification finds a defect.

- [x] Run `make test`, `make lint`, `make check-plugins`, and `make build`. All passed; race-tested coverage is 93.9%, lint reports zero issues.
- [x] Run `GOOS=windows GOARCH=amd64 go build ./...` for executor portability.
- [x] Run an isolated live review with a small input through find, synthesis, and verify; confirm the report and archived model references.
- [x] Run a small mixed-model/variant profile with available catalog entries; verify each intended selection in recorded CLI arguments and any resolved-model events.
- [x] Confirm an invalid variant fails visibly and a timed-out review does not continue in a server.
- [x] Confirm the installed caller skill is discoverable from OpenCode and can invoke the OpenCode-only profile; check Mini caller use without adding a Mini executor.
- [x] Record version, base SHA, checks, live-test scope, and attribution. All required implementation checks passed.

## Acceptance criteria

- The port's ancestry includes the exact stable base and excludes later upstream changes unless separately approved.
- All three review stages support OpenCode, with different models and reasoning variants in one profile.
- The CLI receives native `provider/model#variant` references and never receives `--variant`.
- Bare `opencode` uses the configured default; an inherited Claude model/effort cannot override it.
- Claude/Codex profiles, validation, and reporting retain their existing behavior.
- OpenCode failures do not become clean empty findings.
- Archives contain the actual submitted prompt and truthful model/usage data.
- Timeout cleanup stops owned model work, and concurrent runs do not share mutable parser state.
- The OpenCode skill is installable independently and usable from both TUI and Mini.
- The automated checks pass without model credentials or live model calls.

## Review record

- Initial advice received from `ask` using `caigw/kimi-k3#high`.
- Stable-tag file inventory received from `explore`.
- Draft reviewed by `ask` using `caigw/kimi-k3#high`; verdict: ready after four corrections.
- Corrections applied: reject unknown executors at dispatch and contract selection, specify the owner of the empty-effort rule, and name the cancellation fallback that needs approval.
- Additional changes: baseline checks, direct OpenCode skill discovery, local JSON extraction, and explicit `.jsonl` test-only coverage.
- Site and release documentation remain in scope because repository rules require synchronized executor/profile documentation and fork installation instructions must target the correct source.
- Final review confirmed by `ask` using `caigw/kimi-k3#high`: ready for implementation, with no remaining plan blockers.
- Implementation approved by the user; work started with Task 1.
- Kimi reviewed executor/parser code, pipeline/profile/skill integration, and the final port.
  No blocking findings remained. Final verdict: approved for publication.

## Acceptance evidence

Manual live runs used an isolated temporary directory with an eight-line Python file containing two
calculation errors. Unit tests and CI use mocks only.

- OpenCode v2.3.0; Go 1.27.4; golangci-lint 2.15.0; shellcheck 0.11.0.
  CI retains upstream Go 1.26 and golangci-lint 2.12.2.
- Shipped `opencode-only`: four reviewers, configured `openai/gpt-6-astra#low`, no CLI model argument.
  Synthesis reduced eight reports to two findings; verification confirmed both.
  Duration 46.519 seconds, sources 4/4, none degraded.
- Mixed profile: Astra `#low` and Luna `#low` reviewers, Astra `#medium` synthesis,
  Luna `#medium` verification. Both findings confirmed in 32.443 seconds, sources 2/2, none degraded.
  A temporary wrapper recorded all four exact native CLI model references and no `--variant`.
  Requested references survived in the report and archive; actual model stayed empty.
- OpenCode automatically discovered the repository skill. A complete copied skill also worked in
  `opencode --standalone mini` under a PTY: it ran preflight, launched the prepared OpenCode-only
  single-lens review in the background, received completion, read the JSON, and reported both defects
  with sources 1/1 and none degraded. Synthesis and verification were disabled for this caller check.
  The review and final marker completed; Mini client shutdown then needed the PTY to close after
  SIGTERM did not finish within 15 seconds. This is separate from reviewer `run` cancellation.
- Source files in the fixture were unchanged. Live reports, argv logs and Mini transcript remain
  outside the repository under the session temporary directory.
- Live invalid-variant and five-second hard-timeout runs failed visibly after retry, with every
  source degraded and no clean report. The timeout returned exit 2. No new standalone server
  remained; the same three pre-existing private servers and the user's shared service stayed available.
  The initial negative-check script incorrectly expected JSON from an all-degraded review; the
  stable contract returns an error without a report. The corrected check passed.

## Post-completion

After implementation approval and passing checks:

- Commit the port with attribution and push its stable-based branch to `mschedrin/revmux`.
- Make the maintained OpenCode branch the fork's default. Preserve upstream history rather than force-rewriting `master`.
- Publish a fork release only after its release settings target the user's repositories.
- For each later stable upstream tag, merge that tag into the maintained branch, run the full checks, and record the new base and tested OpenCode version.
- Keep the integration in focused commits so future upstream updates can be reviewed without unrelated changes.
- Move the completed implementation plan to `docs/plans/completed/`.

Creating a Homebrew tap, changing the Go module path, adding shared-server session cancellation, and adding Mini session inspection are separate follow-up work.
