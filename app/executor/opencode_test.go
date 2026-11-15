package executor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revmux/app/executor"
	"github.com/umputun/revmux/app/executor/mocks"
)

func openCodeCapture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opencode-v2-clean.jsonl"))
	require.NoError(t, err)
	return data
}

// openCodeRecords derives variants from the recorded V2 CLI bytes.
func openCodeRecords(t *testing.T) []map[string]any {
	t.Helper()
	data := bytes.TrimSpace(openCodeCapture(t))
	records := make([]map[string]any, 0, bytes.Count(data, []byte("\n"))+1)
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		var record map[string]any
		require.NoError(t, json.Unmarshal(line, &record))
		records = append(records, record)
	}
	return records
}

func openCodeBytes(t *testing.T, records []map[string]any) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, record := range records {
		data, err := json.Marshal(record)
		require.NoError(t, err)
		out.Write(data)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func openCodeAnswer(t *testing.T, text string) []byte {
	t.Helper()
	records := openCodeRecords(t)
	records[5]["part"].(map[string]any)["text"] = text
	return openCodeBytes(t, records)
}

func TestOpenCode_Run(t *testing.T) {
	raw := &bytes.Buffer{}
	sink := discardSink()
	c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, openCodeCapture(t))), executor.Opts{})
	res, err := c.Run(context.Background(), executor.Request{
		Prompt: "review", Model: "provider/model#deep", RawOutput: raw,
	}, sink)
	require.NoError(t, err)
	assert.JSONEq(t, `{"findings":[]}`, string(res.StructuredOutput))
	assert.Equal(t, 17001, res.Tokens, "both steps and cache reads count once")
	assert.Equal(t, "provider/model#deep", res.RequestedModel)
	assert.Empty(t, res.ActualModel, "the CLI supplies no observed model")
	assert.Equal(t, string(openCodeCapture(t)), res.Raw)
	assert.Equal(t, res.Raw, raw.String())
	var tools []string
	for _, call := range sink.EmitCalls() {
		if call.Event.Kind == executor.EventProgress {
			tools = append(tools, call.Event.Text)
		}
	}
	assert.Equal(t, []string{"read scope.md"}, tools)
}

func TestOpenCode_Run_argumentsAndStdin(t *testing.T) {
	for _, model := range []string{"", "provider/model#deep", "ollama/model:4b#custom"} {
		t.Run(model, func(t *testing.T) {
			runner := fakeRunner("echo", "-")
			c := executor.NewOpenCode(runner, executor.Opts{})
			schema := json.RawMessage(`{"type":"object"}`)
			res, err := c.Run(context.Background(), executor.Request{Prompt: "review", Model: model, Schema: schema}, nil)
			require.Error(t, err, "the helper echoes stdin, not a CLI event stream")
			assert.Equal(t, "review"+executor.OpenCodeOutputContract(schema), res.Raw)
			call := runner.CommandCalls()[0]
			assert.Equal(t, "opencode", call.Name)
			expected := []string{"run", "--format", "json", "--auto", "--archived", "--standalone", "--thinking"}
			if model != "" {
				expected = append(expected, "--model", model)
			}
			assert.Equal(t, expected, call.Args)
			assert.NotContains(t, call.Args, "--variant")
		})
	}
	assert.Empty(t, executor.OpenCodeOutputContract(nil))

	runner := fakeRunner("echo", "-")
	_, err := executor.NewOpenCode(runner, executor.Opts{}).Run(context.Background(),
		executor.Request{Model: "provider/model", Effort: "high"}, nil)
	require.ErrorContains(t, err, "#variant")
	assert.Empty(t, runner.CommandCalls(), "an invalid request must not launch a model")
}

func TestOpenCode_Run_answers(t *testing.T) {
	const finding = `{"findings":[{"title":"Preserve {nested} content","body":"An escaped quote: \" and a bracket: ["}]}`
	for _, tt := range []struct {
		name, answer string
		want         string
	}{
		{"clean", `{"findings":[]}`, `{"findings":[]}`},
		{"fenced", "```json\n{\"findings\":[]}\n```", `{"findings":[]}`},
		{"prose", "Result:\n{\"findings\":[]}\nDone.", `{"findings":[]}`},
		{"bracketed path", "Results for [scope.md]:\n{\"findings\":[]}", `{"findings":[]}`},
		{"markdown link", "Results for [file](src/file.go):\n{\"findings\":[]}", `{"findings":[]}`},
		{"multiple labels", "See [src/file.go] and [test results].\n{\"findings\":[]}", `{"findings":[]}`},
		{"label then fenced object", "For [scope.md]:\n```json\n{\"findings\":[]}\n```", `{"findings":[]}`},
		{"finding", finding, finding},
		{"wrapped finding", "Result:\n```json\n" + finding + "\n```\nDone.", finding},
		{"no JSON", "Nothing found.", ""},
		{"nested in truncated outer", `{"findings":[{"title":"incomplete"}`, ""},
		{"nested in malformed outer", `{"findings": invalid, "fallback": {"findings":[]}}`, ""},
		{"nested before syntax error", `{"fallback":{"findings":[]},"findings":invalid}`, ""},
		{"missing comma", `{"note":"invalid" "fallback":{"findings":[]}}`, ""},
		{"fenced malformed outer", "```json\n{\"findings\":invalid,\"fallback\":{\"findings\":[]}}\n```", ""},
		{"later fallback", `{"findings":invalid} {"findings":[]}`, ""},
		{"array wrapper", `[{"findings":[]}]`, ""},
		{"array before object", `[] {"findings":[]}`, ""},
		{"literal array before object", `[false] {"findings":[]}`, ""},
		{"string array before object", `["scope.md"] {"findings":[]}`, ""},
		{"number array before object", `[1] {"findings":[]}`, ""},
		{"malformed array wrapper", `[invalid, {"findings":[]}]`, ""},
		{"malformed array before object", `[false,] {"findings":[]}`, ""},
		{"truncated array wrapper", `[{"findings":[]}`, ""},
		{"nested array wrapper", `[[{"findings":[]}]]`, ""},
		{"label before array", `For [scope.md]: [{"findings":[]}]`, ""},
		{"label before malformed object", `For [scope.md]: {"findings":invalid} {"findings":[]}`, ""},
		{"label without answer", "Results for [scope.md]", ""},
		{"unclosed label", `Results for [scope.md: {"findings":[]}`, ""},
		{"limit words are content", `{"findings":[],"note":"rate limit exceeded"}`, `{"findings":[],"note":"rate limit exceeded"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, openCodeAnswer(t, tt.answer))), executor.Opts{})
			res, err := c.Run(context.Background(), executor.Request{}, nil)
			if tt.want != "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, string(res.StructuredOutput))
				assert.False(t, res.RateLimited)
			} else {
				require.Error(t, err)
				assert.Empty(t, res.StructuredOutput)
			}
		})
	}
}

func TestOpenCode_Run_multipartAnswer(t *testing.T) {
	type part struct{ id, text string }
	for _, tt := range []struct {
		name  string
		parts []part
		want  string
	}{
		{"split answer", []part{{"a", `{"findings":`}, {"b", `[]}`}}, `{"findings":[]}`},
		{"split finding", []part{{"a", `{"findings":[{"title":"`}, {"b", "Keep this"}, {"c", `"}]}`}},
			`{"findings":[{"title":"Keep this"}]}`},
		{"update first in place", []part{{"a", ""}, {"b", `[]}`}, {"a", `{"findings":`}}, `{"findings":[]}`},
		{"repeat last", []part{{"a", `{"findings":`}, {"b", `[]}`}, {"b", `[]}`}}, `{"findings":[]}`},
		{"incomplete outer", []part{{"a", `{"findings":`}, {"b", `{"findings":[]}`}}, ""},
		{"malformed outer", []part{{"a", `{"findings":invalid,"fallback":`}, {"b", `{"findings":[]}}`}}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			records := openCodeRecords(t)
			records[1]["part"].(map[string]any)["text"] = `{"earlier_message":invalid`
			stream := append([]map[string]any{}, records[:5]...)
			reason := maps.Clone(records[5])
			reason["type"] = "reasoning"
			reason["part"] = maps.Clone(records[5]["part"].(map[string]any))
			reason["part"].(map[string]any)["text"] = `{"reasoning":invalid`
			stream = append(stream, reason)
			tool := maps.Clone(records[2])
			tool["part"] = maps.Clone(records[2]["part"].(map[string]any))
			tool["part"].(map[string]any)["messageID"] = records[5]["part"].(map[string]any)["messageID"]
			stream = append(stream, tool)
			for _, p := range tt.parts {
				record := maps.Clone(records[5])
				fields := maps.Clone(records[5]["part"].(map[string]any))
				fields["id"], fields["text"] = p.id, p.text
				record["part"] = fields
				stream = append(stream, record)
			}
			stream = append(stream, records[6])
			c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, openCodeBytes(t, stream))), executor.Opts{})
			res, err := c.Run(context.Background(), executor.Request{}, nil)
			if tt.want == "" {
				require.Error(t, err)
				assert.Empty(t, res.StructuredOutput)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(res.StructuredOutput))
		})
	}
}

func TestOpenCode_Run_streamFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func([]map[string]any) []map[string]any
	}{
		{"no final stop", func(r []map[string]any) []map[string]any { return r[:6] }},
		{"reasoning-only final message", func(r []map[string]any) []map[string]any {
			r[5]["type"] = "reasoning"
			return r
		}},
		{"trailing unfinished work", func(r []map[string]any) []map[string]any {
			last := map[string]any{"type": "step_finish", "part": map[string]any{
				"id": "new-finish", "messageID": r[6]["part"].(map[string]any)["messageID"],
				"reason": "tool-calls", "tokens": r[6]["part"].(map[string]any)["tokens"],
			}}
			return append(r, last)
		}},
		{"tool step only", func(r []map[string]any) []map[string]any { return r[:4] }},
		{"length limit", func(r []map[string]any) []map[string]any {
			r[6]["part"].(map[string]any)["reason"] = "length"
			return r
		}},
		{"missing usage", func(r []map[string]any) []map[string]any {
			delete(r[6]["part"].(map[string]any), "tokens")
			return r
		}},
		{"negative usage", func(r []map[string]any) []map[string]any {
			r[6]["part"].(map[string]any)["tokens"].(map[string]any)["output"] = -1
			return r
		}},
		{"missing message ID", func(r []map[string]any) []map[string]any {
			delete(r[5]["part"].(map[string]any), "messageID")
			return r
		}},
		{"malformed known part", func(r []map[string]any) []map[string]any {
			r[5]["part"] = "invalid"
			return r
		}},
		{"new unfinished step", func(r []map[string]any) []map[string]any { return append(r, r[0]) }},
		{"typed error even with exit zero", func(r []map[string]any) []map[string]any {
			// This object shape was captured from an invalid V2 variant.
			return append(r, map[string]any{"type": "error", "error": map[string]any{
				"type": "provider.no-route", "message": "Variant unavailable for provider/model: invalid",
			}})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := openCodeBytes(t, tt.edit(openCodeRecords(t)))
			c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, data)), executor.Opts{})
			res, err := c.Run(context.Background(), executor.Request{}, nil)
			require.Error(t, err)
			assert.Empty(t, res.StructuredOutput)
			assert.Equal(t, string(data), res.Raw)
		})
	}
	for _, data := range [][]byte{nil, []byte("not json\n"), []byte(`{"type":"text"`), bytes.Repeat([]byte("x"), (8<<20)+1)} {
		c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, data)), executor.Opts{HardTimeout: 20 * time.Second})
		res, err := c.Run(context.Background(), executor.Request{}, nil)
		require.Error(t, err)
		assert.Empty(t, res.StructuredOutput)
		assert.Equal(t, string(data), res.Raw, "read errors still drain and archive the stream")
	}
}

func TestOpenCode_Run_repeatedPartsAndReasoning(t *testing.T) {
	r := openCodeRecords(t)
	// A reasoning event has the same ID/message/text fields, but it is not answer text.
	reason := map[string]any{"type": "reasoning", "part": r[1]["part"]}
	r = append(r, r[3], r[6], reason, map[string]any{"type": "future", "part": "opaque"})
	c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, openCodeBytes(t, r))), executor.Opts{})
	res, err := c.Run(context.Background(), executor.Request{}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"findings":[]}`, string(res.StructuredOutput))
	assert.Equal(t, 17001, res.Tokens, "repeated step events do not double-count usage")
}

func TestOpenCode_Run_stderrAndExit(t *testing.T) {
	for _, output := range [][]byte{nil, openCodeCapture(t)} {
		sink := discardSink()
		c := executor.NewOpenCode(fakeRunner("fail", writeFixture(t, output),
			writeFixture(t, []byte("help text\nUnrecognized flag: --variant\n"))), executor.Opts{})
		res, err := c.Run(context.Background(), executor.Request{}, sink)
		require.ErrorContains(t, err, "Unrecognized flag")
		assert.Equal(t, 3, res.ExitCode)
		assert.Empty(t, res.StructuredOutput)
		for _, call := range sink.EmitCalls() {
			assert.NotContains(t, call.Event.Text, "help text")
		}
	}
}

func TestOpenCode_Run_environment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-provider-key")
	t.Setenv("CLAUDECODE", "nested")
	t.Setenv("PWD", "/caller/checkout")
	for _, kind := range []string{"opencode", "codex"} {
		t.Run(kind, func(t *testing.T) {
			runner := fakeRunner("env", "-")
			var res executor.Result
			if kind == "opencode" {
				res, _ = executor.NewOpenCode(runner, executor.Opts{}).Run(context.Background(), executor.Request{}, nil)
				assert.Contains(t, res.Raw, "ANTHROPIC_API_KEY=test-provider-key")
				assert.NotRegexp(t, `(?m)^PWD=`, res.Raw)
			} else {
				res, _ = executor.NewCodex(runner, executor.Opts{}).Run(context.Background(), executor.Request{}, nil)
				assert.NotContains(t, res.Raw, "ANTHROPIC_API_KEY=")
				assert.Contains(t, res.Raw, "PWD=/caller/checkout")
			}
			assert.NotContains(t, res.Raw, "CLAUDECODE=")
		})
	}
}

func TestOpenCode_Run_workDir(t *testing.T) {
	caller, target := t.TempDir(), t.TempDir()
	t.Chdir(caller)
	t.Setenv("PWD", caller)
	relative, err := filepath.Rel(caller, target)
	require.NoError(t, err)
	for _, tt := range []struct {
		name, workdir, want string
	}{
		{"another checkout", target, target},
		{"relative checkout", relative, target},
		{"inherited directory", "", caller},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := executor.NewOpenCode(fakeRunner("pwd-first", "-"), executor.Opts{WorkDir: tt.workdir})
			res, runErr := c.Run(context.Background(), executor.Request{}, nil)
			require.Error(t, runErr, "the helper reports a path rather than a model stream")
			require.Zero(t, res.ExitCode)
			got, pathErr := filepath.EvalSymlinks(res.Raw)
			require.NoError(t, pathErr)
			want, pathErr := filepath.EvalSymlinks(tt.want)
			require.NoError(t, pathErr)
			assert.Equal(t, want, got, "OpenCode's PWD-first lookup must use the review directory")
		})
	}
}

func TestOpenCode_Run_cancelAndIdle(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent", true: "idle"}[idle], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expire := make(chan func(), 1)
			timer := &mocks.TimerMock{StopFunc: func() bool { return true }, ResetFunc: func(time.Duration) bool { return true }}
			opts := executor.Opts{}
			if idle {
				opts.IdleTimeout = time.Minute
				opts.Clock = &mocks.ClockMock{AfterFuncFunc: func(_ time.Duration, funcToCall func()) executor.Timer {
					expire <- funcToCall
					return timer
				}}
			} else {
				expire <- cancel
			}
			var once sync.Once
			sink := &mocks.EventSinkMock{EmitFunc: func(event executor.Event) {
				if event.Kind == executor.EventActivity {
					once.Do(func() { (<-expire)() })
				}
			}}
			data := openCodeBytes(t, openCodeRecords(t)[:2])
			c := executor.NewOpenCode(fakeRunner("stall", writeFixture(t, data)), opts)
			res, err := c.Run(ctx, executor.Request{}, sink)
			if idle {
				require.NoError(t, err)
				assert.True(t, res.IdleTimedOut)
				assert.NotEmpty(t, timer.ResetCalls())
			} else {
				require.ErrorIs(t, err, context.Canceled)
				assert.False(t, res.IdleTimedOut)
			}
			assert.Empty(t, res.StructuredOutput)
		})
	}
}

func TestOpenCode_Run_hardTimeoutAndStartFailure(t *testing.T) {
	c := executor.NewOpenCode(fakeRunner("stall", writeFixture(t, nil)), executor.Opts{HardTimeout: time.Nanosecond})
	_, err := c.Run(context.Background(), executor.Request{}, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	runner := &mocks.CommandRunnerMock{CommandFunc: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "revmux-no-such-opencode")
	}}
	_, err = executor.NewOpenCode(runner, executor.Opts{}).Run(context.Background(), executor.Request{}, nil)
	require.ErrorContains(t, err, "start opencode")
}

func TestOpenCode_Run_concurrent(t *testing.T) {
	c := executor.NewOpenCode(fakeRunner("emit", writeFixture(t, openCodeCapture(t))), executor.Opts{})
	type outcome struct {
		res executor.Result
		err error
	}
	done := make(chan outcome, 4)
	for range 4 {
		go func() {
			res, err := c.Run(context.Background(), executor.Request{Model: "provider/model#high"}, nil)
			done <- outcome{res: res, err: err}
		}()
	}
	for range 4 {
		out := <-done
		require.NoError(t, out.err)
		assert.Equal(t, 17001, out.res.Tokens)
		assert.Contains(t, string(out.res.StructuredOutput), "findings")
	}
}
