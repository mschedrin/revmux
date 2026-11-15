package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRunner(t *testing.T) {
	tests := []struct {
		name, in string
		want     Runner
	}{
		{"binary alone", "claude", Runner{Executor: "claude"}},
		{"binary and model", "claude/opus", Runner{Executor: "claude", Model: "opus"}},
		{"the whole triple", "codex/gpt-5.6-sol:high", Runner{Executor: "codex", Model: "gpt-5.6-sol", Effort: "high"}},
		{"binary and effort", "codex:xhigh", Runner{Executor: "codex", Effort: "xhigh"}},
		{"empty is empty", "", Runner{}},
		{"surrounding space", "  claude/opus:low  ", Runner{Executor: "claude", Model: "opus", Effort: "low"}},
		// split on the first slash, so a model whose own name carries one arrives intact
		{"model containing a slash", "codex/vendor/model-1", Runner{Executor: "codex", Model: "vendor/model-1"}},
		{"model containing a dot and dashes", "codex/gpt-5.6-sol", Runner{Executor: "codex", Model: "gpt-5.6-sol"}},
		{"OpenCode default", "opencode", Runner{Executor: "opencode"}},
		{"OpenCode model", "opencode/openai/reviewer", Runner{Executor: "opencode", Model: "openai/reviewer"}},
		{"OpenCode variant", "opencode/openai/reviewer#high", Runner{Executor: "opencode", Model: "openai/reviewer#high"}},
		{"custom variant", "opencode/provider/reviewer#deep", Runner{Executor: "opencode", Model: "provider/reviewer#deep"}},
		{"nested model", "opencode/router/vendor/reviewer#low", Runner{Executor: "opencode", Model: "router/vendor/reviewer#low"}},
		{"colon model tag", "opencode/ollama/gemma3:4b#custom", Runner{Executor: "opencode", Model: "ollama/gemma3:4b#custom"}},
		{"colon is not effort", "opencode/local/model:high", Runner{Executor: "opencode", Model: "local/model:high"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRunner(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseRunner_Errors(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"unknown binary", "gemini/pro", `unknown executor "gemini"`},
		// one way to say "the binary's default model", not two
		{"trailing slash", "claude/", "no model after the slash"},
		{"trailing slash with effort", "claude/:high", "no model after the slash"},
		{"model with no binary", "opus", `unknown executor "opus"`},
		{"empty binary", "/opus", `unknown executor ""`},
		// folding it into the model name would leave a typo nobody notices until the run behaves oddly
		{"misspelled effort", "claude/opus:hgih", `unknown effort "hgih"`},
		{"effort where a model belongs", "claude:opus", `unknown effort "opus"`},
		{"OpenCode legacy effort", "opencode:high", "not :effort"},
		{"OpenCode no provider", "opencode/model", "invalid OpenCode model"},
		{"OpenCode empty reference", "opencode/", "invalid OpenCode model"},
		{"OpenCode empty provider", "opencode//model", "invalid OpenCode model"},
		{"OpenCode empty model", "opencode/provider/", "invalid OpenCode model"},
		{"OpenCode variant without model", "opencode/provider/#high", "invalid OpenCode model"},
		{"OpenCode empty variant", "opencode/provider/model#", "invalid OpenCode variant"},
		{"OpenCode repeated variant", "opencode/provider/model#high#low", "invalid OpenCode variant"},
		{"OpenCode model space", "opencode/provider/two words", "invalid OpenCode model"},
		{"OpenCode variant space", "opencode/provider/model#two words", "invalid OpenCode variant"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRunner(tt.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// a model belongs to a binary, so inheriting one across binaries is how `opus` reaches codex
func TestRunner_or(t *testing.T) {
	base := Runner{Executor: "claude", Model: "opus", Effort: "high"}

	t.Run("nothing of its own takes the fallback whole", func(t *testing.T) {
		assert.Equal(t, base, Runner{}.or(base))
	})

	t.Run("the same binary fills in what it omits", func(t *testing.T) {
		assert.Equal(t, base, Runner{Executor: "claude"}.or(base))
		assert.Equal(t, Runner{Executor: "claude", Model: "sonnet", Effort: "high"},
			Runner{Executor: "claude", Model: "sonnet"}.or(base))
	})

	t.Run("a different binary never inherits the model", func(t *testing.T) {
		assert.Equal(t, Runner{Executor: "codex", Effort: "high"}, Runner{Executor: "codex"}.or(base))
		assert.Equal(t, Runner{Executor: "codex", Model: "gpt-5.6-sol", Effort: "low"},
			Runner{Executor: "codex", Model: "gpt-5.6-sol", Effort: "low"}.or(base))
	})

	t.Run("effort carries across binaries, since it belongs to neither model", func(t *testing.T) {
		assert.Equal(t, "high", Runner{Executor: "codex"}.or(base).Effort)
	})
	t.Run("OpenCode does not inherit a foreign model or effort", func(t *testing.T) {
		assert.Equal(t, Runner{Executor: "opencode"}, Runner{Executor: "opencode"}.or(base))
	})
	t.Run("OpenCode inherits its own complete reference", func(t *testing.T) {
		native := Runner{Executor: "opencode", Model: "provider/model#deep"}
		assert.Equal(t, native, Runner{Executor: "opencode"}.or(native))
		assert.Equal(t, native, Runner{}.or(native))
		explicit := Runner{Executor: "opencode", Model: "provider/other"}
		assert.Equal(t, explicit, explicit.or(native), "the previous model's variant must not carry over")
		assert.Equal(t, Runner{Executor: "claude"}, Runner{Executor: "claude"}.or(native))
	})
}
