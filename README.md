# revmux

[![build](https://github.com/mschedrin/revmux/actions/workflows/ci.yml/badge.svg?branch=feat%2Fopencode-v2)](https://github.com/mschedrin/revmux/actions/workflows/ci.yml)

**[Fork source](https://github.com/mschedrin/revmux)** · [Documentation](site/docs.html) · [Reference](site/reference.html) · [Releases](https://github.com/mschedrin/revmux/releases)

This fork adds OpenCode V2 support to upstream `master`
at `8c00418688a92032a976362b2a21973fb62583d5`.
The maintained branch is `feat/opencode-v2`. OpenCode integration was tested with **v2.3.0**.

revmux runs a structured multi-agent review. It supervises Claude, Codex and `opencode run`
subprocesses, then returns findings on stdout as JSON or markdown.

**It is normally launched by a coding agent rather than typed by you.** The [shipped skill](#agent-skills)
works out what is under review, writes the context to disk, runs revmux and reads the report back. To that
agent revmux is a black box with a stable contract, context in and a verified report out, which is why JSON
is the default and `--markdown` is the opt-in for when a person is reading.

**The subject does not have to be code.** A branch, a pull request, an implementation plan, a design document
or a proposal all go in the same way. Triage is a job of its own: pointed at a filed issue or discussion, the
`triage` profile runs a four-way panel over it, rating how much each point bears on the decision rather than
what breaks at runtime, and returns the arguments for a maintainer to weigh instead of a verdict.

revmux runs a review and returns findings, and does nothing else. It performs no scope detection, no git
operations, no PR fetching and no source modification. All review context is written to disk by the caller
and passed in as a task round, named with `--task` and `--run`.

Fork documentation is in **[site/docs.html](site/docs.html)**, with flags in the
**[reference](site/reference.html)**. The published revmux.com site describes upstream.

![revmux running four agents, shown as a terminal overlay above the session that launched it](site/assets/screenshot-run.webp)

A status table with one row per supervised process, a combined chronological pane and a tab per agent. The
shipped launcher opens it in a terminal overlay, because an agent's shell has no tty.

## Why

**Every agent is visible, and every phase recoverable.** Each model runs as a subprocess revmux owns, so it
can be watched, timed, killed and relaunched: a watchdog on each one, a live view of what it is doing and
what it has spent, one automatic retry, and a run archive to debug a bad review afterwards. Fan-out driven
from inside an AI coding session has none of that, and agents that go quiet for minutes cannot be recovered
from there. A subprocess does not make the model faster; it makes the run something the caller can hold on
to.

**One review standard rather than one per session.** A review assembled ad hoc varies with the prompt, the
context left in the session, and whatever the model decided to look at that time. Here the roster, the
lenses, the severity bar and the three stages are files, so two runs of a profile ask the same questions of
the code. Checked into `.revmux/`, they are versioned and diffable, and a contributor gets the review a
maintainer would have run.

**Use the right model for each role.** A broad bug pass, an adversarial second opinion, synthesis and
verification can favor different models, effort levels, latency and cost. Each finder selects its own binary,
model and effort, and synthesis and verification can override those choices separately. The resolved runner
for every process is recorded in the run archive.

**Auditable by agents, not only by people.** The archive keeps the composed prompt each agent received, the
verbatim output each one returned, the findings after every stage, and revmux's own decisions about stalls
and retries. A person reads the report; an agent can read the entire run and propose edits to the lens and
profile text behind it.

**Rounds stack.** Reviewing a branch or a PR is never one review; it is a review, some fixes, and another
review. revmux keeps the rounds under one task and hands the earlier ones to every agent in the next, with an
instruction to judge them independently rather than confirm them.

## Install

Build this fork with Go 1.26 or newer. The upstream Homebrew package does not contain this port.
No fork Homebrew tap is configured.

```
git clone --branch feat/opencode-v2 https://github.com/mschedrin/revmux.git
cd revmux
make build        # produces .bin/revmux
make install      # and symlinks it to /usr/local/bin/revmux
```

`make install` links rather than copies, so a later `make build` is picked up without reinstalling. Override
the location with `BINDIR` when `/usr/local/bin` is not writable. `make uninstall` removes the link.

**Then install the skill, which is what asks for a review.** The binary runs one; the skill is how your
coding agent drives it:

```sh
# Claude Code
claude plugin marketplace add mschedrin/revmux
claude plugin install revmux@revmux

# Codex
codex plugin marketplace add mschedrin/revmux
codex plugin add revmux@revmux

# OpenCode: from this clone, copy the complete skill directory
mkdir -p ~/.config/opencode/skills
cp -R .opencode/skills/revmux ~/.config/opencode/skills/
```

Install the skill through one marketplace only. If you previously copied or symlinked the Codex skill into
`~/.codex/skills/revmux`, remove that copy after installing the plugin. Then ask for a review in words:
revmux this branch, revmux pr 123, re-review after fixes. See [Agent skills](#agent-skills) for what it does.

revmux drives the model CLIs as subprocesses, so whichever ones your profile names must already be installed
and authenticated: both for `comprehensive`, `focused`, `final`, `grill-me`, `triage` and `expert`, claude
alone for `claude-only`, codex alone for `codex-only`, opencode alone for `opencode-only`. `preflight.sh` in the shipped skill answers it for any
profile and any invocation.

For Claude and Codex, `ANTHROPIC_API_KEY` is stripped from the child environment by default so `claude` uses interactive
subscription auth; pass `--preserve-anthropic-api-key` if you authenticate by key.
`claude` also runs with `--setting-sources project`, so your user `settings.json` is not loaded, and an
`apiKeyHelper` set there never reaches the agents. Put the helper in the reviewed repo's
`.claude/settings.json`, or put a `claude` wrapper first on PATH that runs the real binary, by absolute path,
with `--settings <file>` added.

### OpenCode models, reasoning and Mini

Ask OpenCode to “review this branch with revmux, OpenCode only” from the full TUI or `opencode mini`.
The project skill is discovered in this clone; the global copy makes it available in other projects.
OpenCode loads skills with the same tools in both interfaces. Mini is a caller, not a reviewer executor.

`opencode-only` runs four lens groups and both model stages on `openai/gpt-6.2-sol#high`.
Custom profiles can select models independently. These are placeholder IDs; use your current catalog:

```yaml
model: opencode/provider/finder#high
agents:
  - {name: bugs, lenses: [bugs], color: cyan}
  - {name: peer, lenses: [impl], model: "opencode/other/model#deep", color: green}
stages:
  synthesis: opencode/provider/synthesizer#high
  verify: opencode/local/model:tag#custom
```

The grammar is `opencode/<provider>/<model>[#<variant>]`. It preserves colon tags and nested model
paths. `#variant` selects reasoning; OpenCode does not use revmux's Claude/Codex `:effort` suffix.
Bare `opencode` inherits an OpenCode model from a lower-priority layer, or uses the configured default.
An explicit model replaces the full reference, including its variant. Legacy effort is never inherited.

Review subprocesses use `run --format json --auto --archived --standalone --thinking`.
Prompts go to stdin. Each attempt has a fresh archived session and private server; cancellation
closes that server after the client exits. `--thinking` supplies reasoning activity, not effort selection.
Models with longer silent intervals may need `--idle-timeout 5m`; retain a hard timeout.
OpenCode keeps its configured credentials, including `ANTHROPIC_API_KEY`.
The read-only review rule is a prompt instruction, not a sandbox.
The V2 CLI stream does not report the resolved model, so `actual_model` stays empty.

## Quick start

`revmux new` creates the round and prints every path you write to, so nothing constructs a path by hand:

```console
$ revmux new --task pr-123 --run 01-initial
{
  "task_dir": "/abs/.revmux/tasks/pr-123",
  "task_file": "/abs/.revmux/tasks/pr-123/task.md",
  "round_dir": "/abs/.revmux/tasks/pr-123/01-initial",
  "input_dir": "/abs/.revmux/tasks/pr-123/01-initial/input",
  "scope": "/abs/.revmux/tasks/pr-123/01-initial/input/scope.md",
  "created": ["task_dir", "task_file", "round_dir", "input_dir"]
}
```

Take the `scope` path out of that payload rather than joining it yourself, write the scope into it, and run
the review:

```
scope=$(revmux new --task pr-123 --run 01-initial | jq -r .scope)
cat > "$scope" <<'EOF'
Review the changes on this branch against master.
Diff command: git diff master...HEAD
EOF

revmux --task pr-123 --run 01-initial
```

That runs the `comprehensive` profile, shows a live TUI, and writes the report to stdout as JSON. After
fixing something, open a new round on the same task and revmux carries the earlier rounds into every prompt:

```
revmux new --task pr-123 --run 02-after-fix     # then write its own input/scope.md
revmux --task pr-123 --run 02-after-fix > findings.json
```

## How it works

Three fixed stages. Only the roster and the severity bar vary between review shapes, so everything else is
configuration.

1. **find**. The profile's roster runs in parallel: several `claude` agents, each composing one or more
   lenses, plus a `codex` peer. Launch is staggered, agent 1 first and the rest released once it produces
   its first output. Each agent returns structured findings.
2. **synthesize**. One model call. It merges every source's findings, dedupes on `(file, line ±2)`, boosts
   confidence where distinct sources corroborate, and splits out open questions and pre-existing issues.
   It drops nothing: a single-source, low-confidence finding still reaches verify.
3. **verify**. Parallel agents grouped by directory. Each verifier sees only its own group, so it cannot
   anchor on a neighbouring finding. Every finding comes back with a verdict: confirmed, refined, rejected,
   immaterial or pre-existing.

**Codex is a peer source, not a second pass.** It runs alongside the lens agents and its findings go through
the same synthesis and verification. Ordering the two would mean the second reviewer sees the first's
findings and anchors on them, which is exactly what the cross-source confidence boost assumes did not happen.

**A source is a process.** The confidence boost counts distinct processes, never lenses. An agent carrying
two lenses that flags the same issue under both is still one source: it cannot corroborate itself. revmux
stamps the attribution itself once the output is parsed; no schema exposes it to the model.

**Degrade, never abort.** A stalled or crashed **finder** is killed, retried once, and on a second failure
marked degraded while the run continues. The report banner names the missing agent, and synthesis is told the
real source count. A run where *every* source degraded is a tool error, not a clean empty report.
Verification is single-attempt instead: a verifier that fails leaves its group `unverified` rather than being
relaunched, and never drops the findings it was given.

## Task rounds

Review context reaches revmux only as a task round the caller has filled. `--task` names a task under
`--tasks-dir` (default `./.revmux/tasks`), and `--run` names one round inside it. Both names are
caller-chosen and semantic; revmux allocates neither.

```
<tasks-dir>/pr-123/               a task: one subject, reviewed over as many rounds as it takes
├── task.md                       optional; front matter identifying the task
├── 01-initial/                   a round
│   ├── input/                    caller-written; the only channel this round's own context travels through
│   │   ├── scope.md              {{SCOPE}}    required
│   │   ├── goal.md               {{GOAL}}     optional
│   │   ├── profile.md            {{PROFILE}}  optional; non-empty overrides ./.revmux/profile.md
│   │   └── context/              {{CONTEXT}}  optional directory
│   └── ...                       revmux-written artifacts: manifest, prompts, stages, events, tees
└── 02-after-fix/                 the next round, with its own input/
```

Variables expand to the **paths** of these files, never to their contents, so no prompt can be bloated by a
large scope. Context belongs to the round rather than to the task, because round 2 reviews the fixes for what
round 1 found. A round that has already run is an error rather than an overwrite.

`profile.md` is the exception, because what it says is true of the repository rather than of one round. Put
it in `./.revmux/profile.md` and every round without a non-empty override inherits it — `revmux config`
reports the resolved path as `paths.profile_fallback`, and the bytes are copied into each round as
`prompts/input-profile.md` so the archive records what actually calibrated it. Write a round's own
`input/profile.md` only to hold that subject to a different bar. A non-empty round file wins outright.

Every run writes its own artifacts into that round beside the caller's `input/`: `manifest.json` with the
resolved roster and prompt provenance, the composed prompt each agent received, the findings after every
stage, an `events.jsonl` of stalls and retries, and the verbatim output of every agent. That is what makes a
review auditable without re-running it. [The archive layout](https://revmux.com/docs#archive) has the detail.

## Output

The report goes to **stdout** as JSON, or as markdown with `--markdown`. The TUI renders to the tty and
progress lines go to stderr, so `revmux --task pr-123 --run 02-after-fix > findings.json` works with the
display running.

```json
{
  "scope": {"task": "pr-123", "run": "02-after-fix", "scope_path": "..."},
  "sources": {
    "expected": 4, "reported": 3, "degraded": ["docs+tests"],
    "agents": [
      {"name": "bugs+impl", "lenses": ["bugs", "impl"], "executor": "claude",
       "requested_model": "opus", "actual_model": "claude-opus-5",
       "effort": "high", "tokens": 48210, "raised": 6, "degraded": false}
    ]
  },
  "findings": [
    {"id": "f1", "file": "app/pipeline/find.go", "line": 88, "end_line": 0,
     "severity": "major", "confidence": 90,
     "title": "…", "body": "…", "fix": "…",
     "sources": ["bugs+impl", "adversarial"], "lenses": ["bugs", "adversarial"],
     "verdict": "confirmed"}
  ],
  "open_questions": [], "pre_existing": [], "immaterial": [],
  "stats": {"duration_ms": 333000, "tokens": 184920, "stages": []}
}
```

`sources` holds **agent names** and is the only input to the confidence boost. `lenses` holds the lens names
that raised the finding and is informational: it answers "why was this reported", never "how many
independently agreed".

| Code | Meaning |
|---|---|
| `0` | no findings above `--min-confidence` |
| `1` | findings above `--min-confidence`, a normal outcome rather than a failure |
| `2` | tool error: bad config, an unusable round, an unwritable artifact, or every source degraded |

[Every field, verdict and exit condition](https://revmux.com/reference#json) is in the reference.

## Profiles

A profile is roster front matter plus a body that is the shared preamble and severity bar. Every roster entry
composes lenses, and one `model:` string selects the binary, the model and the effort together, so claude and
codex mix inside one review.

| profile | roster |
|---|---|
| `comprehensive` | `bugs+impl`, `arch+quality`, `docs+tests` on claude plus an adversarial codex peer |
| `focused` | one `bugs` agent plus the codex peer, for a small or time-boxed change |
| `final` | `bugs+impl` plus the codex peer, nothing below major reported |
| `claude-only` | the same four lens splits on claude, for a machine with no codex |
| `codex-only` | the same splits on codex, and synthesis and verify with them |
| `opencode-only` | four OpenCode lens groups, with OpenCode synthesis and verification |
| `grill-me` | two lens splits, each run once on claude and once on codex |
| `expert` | two agents at xhigh, each carrying all eight code lenses |
| `triage` | a four-way panel over a filed item rather than a diff |

**The eight are starting points, not the menu.** A profile is a file under `prompts/profiles/`, so dropping
`.revmux/prompts/profiles/release.md` into a project makes `--profile release` work with no registration step
anywhere. Its roster can be as wide as you are willing to pay for, each entry carries whatever lenses the job
needs, and any entry can leave the profile's model for its own. Lenses resolve the same way, so a roster
naming `payments` picks up `.revmux/lenses/payments.md`, written by whoever knows what goes wrong in that
code.

```yaml
---
description: pre-release pass over the payment path
model: claude/opus:high
agents:
  - {name: money,     lenses: [bugs, impl, payments],   color: red}
  - {name: contracts, lenses: [architecture, docs],     color: cyan}
  - {name: peer,      lenses: [adversarial], model: codex/gpt-6.2-sol:xhigh}
  - {name: second,    lenses: [bugs],        model: codex/gpt-6.2-sol:high}
stages:
  synthesis: claude/opus:high
  verify:    claude/sonnet:low
---
```

Prompt text resolves per file across three layers: `./.revmux/`, then `~/.config/revmux/`, then the
defaults built into the binary. `revmux init` copies each prompt file down into `./.revmux/` from the layer
that won it, so there is something local to edit, and writes the config from the shipped commented template
rather than from what resolved. `--dump-defaults <dir>` extracts the embedded tree for a diff.

**Checked in, `.revmux/` is the project's review standard.** What a project cares about, its conventions,
what counts as major, the mistakes it keeps repeating, usually lives in a maintainer's head and reaches
contributors one review comment at a time. Committed, it is versioned and diffable like the rest of the code:
a contributor who clones the repository gets the review a maintainer would have run, a finding traces back to
the lens text that raised it, and a review that missed something is fixed by editing a file, once.

**`.revmux/` is also code.** A checked-in lens becomes the instructions a headless agent with a shell
executes, so running revmux inside a repository trusts it the same way `.claude/` or a `Makefile` there does.
Review it before reviewing a branch you did not write.

The [profiles](https://revmux.com/docs#profiles) and [lenses](https://revmux.com/docs#lenses) sections cover
the roster keys, the model grammar and what each of the thirteen lenses looks for.

## Subcommands

All five print JSON on stdout and exit before any review starts.

| command | does |
|---|---|
| `revmux config` | reports the resolved configuration: knobs with their precedence layer, every profile, lens and stage, and the task store |
| `revmux new` | creates a task, a round and its `input/`, and prints every path plus which of them it created |
| `revmux init` | copies each prompt file down from the layer that won it, reporting which one that was, and writes the config from the shipped template |
| `revmux stats` | arithmetic over the archive: per agent, per lens, per stage and per task |
| `revmux cleanup` | removes one named task and everything under it, the only command that deletes anything |

## Terminal UI

A status table with one row per supervised process, a combined chronological pane and a tab per agent, then a
findings browser when the report is in. `i` shows the inputs the round was pointed at. With `--no-tui`, or
when the tty cannot be opened, the same events render as timestamped lines on stderr.

```
16:02:11 bugs+impl     started [bugs, impl]
16:04:02 docs+tests    retrying: agent docs+tests stalled
16:05:12 bugs+impl     done, 6 findings
16:09:03               ── complete ──
16:09:03               6m52s, sources 4/4, degraded none
16:09:03               6 findings: 1 major, 5 minor
```

[Keys and panes](https://revmux.com/docs#tui) are documented on the site.

## Agent skills

revmux is built to be driven by a caller model, and this repository ships that caller as a skill for three
harnesses. Ask for a review in words and the skill does the rest: it resolves what is being reviewed, runs the
git commands, writes the round's `input/`, launches revmux, reads the JSON back, and opens a new round on the
same task after fixes.

| harness | location | install |
|---|---|---|
| Claude Code | `.claude-plugin/skills/revmux/` | `claude plugin marketplace add mschedrin/revmux` then `claude plugin install revmux@revmux` |
| Codex CLI | `plugins/codex/skills/revmux/` | `codex plugin marketplace add mschedrin/revmux` then `codex plugin add revmux@revmux` |
| OpenCode TUI / Mini | `.opencode/skills/revmux/` | Copy the complete directory to `~/.config/opencode/skills/revmux/` |

All three carry the same reference material and scripts: `preflight.sh` checks the binaries an invocation
needs, `task-state.sh` reports what a task holds, `launch-revmux.sh` runs revmux with its TUI in a terminal
overlay, and `analyze-corpus.py` reads the archive back as numbers about the review itself.

## Development

```
make build    # build .bin/revmux
make install  # symlink .bin/revmux into $BINDIR (default /usr/local/bin)
make test     # race detector plus coverage, mocks excluded
make lint     # golangci-lint plus shellcheck over the shipped scripts
make fmt      # gofmt and goimports
```

No test spawns a real model. The executors are driven through a mocked `CommandRunner` against recorded CLI
fixtures, the pipeline through mocked runners, and the TUI through synthetic bubbletea messages.

## License

MIT

Upstream revmux is by umputun. The OpenCode adapter was adapted from
[hackmajoris/revmux PR #1](https://github.com/hackmajoris/revmux/pull/1),
commit `319c81ac15e83da999924c7f14f8637f74bd62f6`, and updated for OpenCode V2.
The upstream license is retained.
