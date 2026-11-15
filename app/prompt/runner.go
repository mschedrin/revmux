package prompt

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Runner is one authored `model:` value, resolved: which binary, which of its models, and how hard it
// thinks. One field carries all three because they are not independent — `opus` means nothing to codex,
// so separate fields let a file declare a pairing that cannot run.
type Runner struct {
	Executor string
	Model    string
	Effort   string
}

// parseRunner reads Claude/Codex `<executor>[/<model>][:<effort>]` and OpenCode
// `opencode[/<provider>/<model>[#<variant>]]`. The executor is mandatory for non-empty
// selections, and a trailing slash is invalid. Claude/Codex split on the first slash
// and last colon; their effort suffix must be known. OpenCode preserves native model
// paths, colon tags and variants in Model, and leaves Effort empty.
func parseRunner(s string) (Runner, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Runner{}, nil
	}
	// OpenCode uses provider/model#variant. A colon is part of its model ID,
	// not the legacy effort suffix (for example, ollama/gemma3:4b).
	if s == executorOpenCode || strings.HasPrefix(s, executorOpenCode+"/") {
		return parseOpenCodeRunner(s)
	}
	if strings.HasPrefix(s, executorOpenCode+":") {
		return Runner{}, errors.New("OpenCode uses opencode/provider/model#variant, not :effort")
	}

	out := Runner{}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		out.Effort, s = s[i+1:], s[:i]
		if !slices.Contains(efforts, out.Effort) {
			return Runner{}, fmt.Errorf("unknown effort %q, want one of %v", out.Effort, efforts)
		}
	}

	var found bool
	out.Executor, out.Model, found = strings.Cut(s, "/")
	// a trailing slash is a second spelling of the binary alone, and an accepted second spelling is one
	// nobody agrees on: `claude/` would inherit a fallback model or fall to the binary's default while
	// looking like it named one
	if found && out.Model == "" {
		return Runner{}, fmt.Errorf("no model after the slash in %q, write the binary alone for its default", s)
	}
	if !slices.Contains(executors, out.Executor) {
		return Runner{}, fmt.Errorf("unknown executor %q in %q, want one of %v", out.Executor, s, executors)
	}
	return out, nil
}

func parseOpenCodeRunner(s string) (Runner, error) {
	out := Runner{Executor: executorOpenCode}
	if s == executorOpenCode {
		return out, nil
	}
	ref := strings.TrimPrefix(s, executorOpenCode+"/")
	base, variant, hasVariant := strings.Cut(ref, "#")
	provider, model, hasModel := strings.Cut(base, "/")
	if !hasModel || provider == "" || model == "" || strings.ContainsAny(provider, ": \t\r\n") ||
		strings.ContainsAny(model, " \t\r\n") || strings.HasSuffix(model, "/") {
		return Runner{}, fmt.Errorf("invalid OpenCode model %q, want opencode/provider/model[#variant]", s)
	}
	if hasVariant && (variant == "" || strings.ContainsAny(variant, "# \t\r\n")) {
		return Runner{}, fmt.Errorf("invalid OpenCode variant in %q, use one non-empty #variant", s)
	}
	out.Model = ref
	return out, nil
}

// or returns r with anything it omits taken from fallback. The executor is the one field that cannot be
// inherited piecemeal: an entry naming a binary brings its own model, or it would run the fallback's
// model on the wrong binary — the pairing this whole type exists to prevent.
func (r Runner) or(fallback Runner) Runner {
	if r.Executor == "" {
		return fallback
	}
	if r.Executor == executorOpenCode {
		if r.Model == "" && fallback.Executor == executorOpenCode {
			r.Model = fallback.Model
		}
		// Native variants belong to the selected model. Never inherit a legacy effort.
		r.Effort = ""
		return r
	}
	if r.Executor != fallback.Executor {
		// a different binary: only the effort, which is not a property of either model, can carry over
		if r.Effort == "" {
			r.Effort = fallback.Effort
		}
		return r
	}
	if r.Model == "" {
		r.Model = fallback.Model
	}
	if r.Effort == "" {
		r.Effort = fallback.Effort
	}
	return r
}
