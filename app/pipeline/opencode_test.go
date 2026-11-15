package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revmux/app/executor"
	"github.com/umputun/revmux/app/finding"
	"github.com/umputun/revmux/app/pipeline/mocks"
)

func TestArchivedPrompt_executors(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
	}{
		{"claude", executor.ClaudeNarrationContract(finding.FinderSchema())},
		{"codex", executor.CodexOutputContract(finding.FinderSchema())},
		{"opencode", executor.OpenCodeOutputContract(finding.FinderSchema())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, err := archivedPrompt(tt.name, "review", finding.FinderSchema())
			require.NoError(t, err)
			assert.Equal(t, "review"+tt.want, text)
		})
	}
	for _, name := range []string{"", "unknown"} {
		text, err := archivedPrompt(name, "review", finding.FinderSchema())
		require.ErrorContains(t, err, "unknown executor")
		assert.Empty(t, text)
	}
}

func TestPipeline_OpenCodeStages(t *testing.T) {
	for _, peer := range []string{"opencode/provider/peer#low", "codex/peer:low"} {
		t.Run(peer, func(t *testing.T) {
			profile := `---
model: opencode/provider/finder#deep
agents:
  - {name: bugs, lenses: [bugs]}
  - {name: impl, lenses: [impl], model: "` + peer + `"}
stages:
  synthesis: opencode/provider/synthesis#custom
  verify: opencode/local/verifier:tag#low
---
review`
			h, seen := artifactHarness(t, map[string]string{"prompts/profiles/testprof.md": profile})
			inner := h.cfg.NewRunner
			var mu sync.Mutex
			var selected []RunnerSpec
			h.cfg.NewRunner = func(spec RunnerSpec) Runner {
				runner := inner(spec)
				return &mocks.RunnerMock{RunFunc: func(ctx context.Context, req executor.Request, sink executor.EventSink) (executor.Result, error) {
					mu.Lock()
					selected = append(selected, spec)
					mu.Unlock()
					assert.Equal(t, spec.Model, req.Model)
					assert.Equal(t, spec.Effort, req.Effort)
					return runner.Run(ctx, req, sink)
				}}
			}
			p := New(h.cfg)
			drain(p)
			report, err := p.Run(context.Background())
			require.NoError(t, err)
			require.Len(t, report.Findings, 4)
			assert.Contains(t, selected, RunnerSpec{Executor: "opencode", Model: "provider/finder#deep"})
			assert.Contains(t, selected, RunnerSpec{Executor: "opencode", Model: "provider/synthesis#custom"})
			assert.Contains(t, selected, RunnerSpec{Executor: "opencode", Model: "local/verifier:tag#low"})
			for _, stat := range report.Sources.Agents {
				if stat.Executor == "opencode" {
					assert.Contains(t, stat.RequestedModel, "#")
					assert.Empty(t, stat.ActualModel)
					assert.Empty(t, stat.Effort)
				}
			}
			for _, stage := range report.Stats.Stages {
				if stage.Name != stageFind {
					assert.Equal(t, "opencode", stage.Executor)
					assert.Contains(t, stage.Model, "#")
					assert.Empty(t, stage.Effort)
				}
			}
			for name, content := range h.archived {
				if strings.HasPrefix(name, "prompts/") {
					assert.Contains(t, seen(), content.String(), name)
				}
			}
		})
	}
}

func TestPipeline_OpenCodeRetry(t *testing.T) {
	profile := strings.Replace(oneAgentProfile, "claude/opus", "opencode/provider/model#deep", 1)
	h := newHarnessWith(t, map[string]string{"prompts/profiles/testprof.md": profile})
	attempts := 0
	h.cfg.NewRunner = func(spec RunnerSpec) Runner {
		assert.Equal(t, "opencode", spec.Executor)
		return &mocks.RunnerMock{RunFunc: func(_ context.Context, req executor.Request, _ executor.EventSink) (executor.Result, error) {
			attempts++
			assert.Equal(t, "provider/model#deep", req.Model)
			_, _ = req.RawOutput.Write([]byte("raw attempt\n"))
			if attempts == 1 {
				return executor.Result{Tokens: 2}, errors.New("provider failed")
			}
			return executor.Result{Tokens: 3, StructuredOutput: findingsJSON()}, nil
		}}
	}
	p := New(h.cfg)
	drain(p)
	report, err := p.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 5, report.Stats.Tokens)
	require.NotNil(t, h.get("agents/solo.jsonl"))
	require.NotNil(t, h.get("agents/solo.retry.jsonl"))
	assert.Equal(t, "raw attempt\n", h.get("agents/solo.retry.jsonl").String())
}

func TestFinder_unknownExecutor(t *testing.T) {
	h := newHarness(t)
	spec := h.cfg.Roster[0]
	spec.Executor = "unknown"
	f := h.finder(func(Event) {})
	res := f.runAgent(context.Background(), spec, 0)
	require.ErrorContains(t, res.err, "unknown executor")
	assert.Nil(t, h.get("prompts/agents/bugs.md"))
}
