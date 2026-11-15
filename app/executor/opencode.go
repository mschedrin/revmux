package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// OpenCode runs the V2 CLI with a private server and native model#variant selection.
// The initial adapter design comes from hackmajoris/revmux PR #1 (MIT).
type OpenCode struct {
	proc
}

// NewOpenCode constructs an executor that can serve concurrent review requests.
func NewOpenCode(runner CommandRunner, opts Opts) *OpenCode {
	return &OpenCode{proc: newProc("opencode", runner, opts)}
}

// Run submits one fresh review. The private server exits when its stdio client closes,
// including after a forced client termination; a shared server would outlive that client.
func (o *OpenCode) Run(ctx context.Context, req Request, sink EventSink) (Result, error) {
	if req.Effort != "" {
		return Result{}, errors.New("OpenCode reasoning must use provider/model#variant, not Request.Effort")
	}
	req.Prompt += OpenCodeOutputContract(req.Schema)
	state := openCodeStream{messages: map[string]*openCodeMessage{}, steps: map[string]int{}}
	var diagnostic string
	res, err := o.run(ctx, req, runSpec{
		argv: o.args(req), sink: sink,
		parse: func(ctx context.Context, r io.Reader) Result {
			readErr := o.readLines(ctx, r, func(line string) {
				if state.err == nil {
					state.err = state.line(line, func(ev Event) { o.emit(sink, ev) })
				}
			})
			if readErr != nil && ctx.Err() == nil {
				_, _ = io.Copy(io.Discard, r)
			}
			state.err = errors.Join(state.err, readErr)
			return Result{Tokens: state.tokens}
		},
		stderrLine: func(line string) {
			if text := strings.TrimSpace(line); text != "" {
				diagnostic = clampRunes(text)
			}
		},
	})
	res.RequestedModel = req.Model
	// V2's CLI stream does not report the resolved model. Do not copy the requested
	// value into ActualModel: that would claim evidence the stream did not supply.
	if err != nil || res.IdleTimedOut {
		return res, err
	}
	if state.err != nil {
		return res, fmt.Errorf("opencode output: %w", state.err)
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("opencode exited %d: %s", res.ExitCode, diagnostic)
	}
	if state.finalMessage == "" {
		return res, errors.New("opencode ended without a completed answer")
	}
	message := state.messages[state.finalMessage]
	if message == nil {
		return res, errors.New("opencode completed without answer text")
	}
	res.StructuredOutput, err = openCodeJSON(message.text())
	return res, err
}

func (o *OpenCode) args(req Request) []string {
	argv := []string{"run", "--format", "json", "--auto", "--archived", "--standalone", "--thinking"}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	return argv
}

// OpenCodeOutputContract supplies the stage's schema and is also used by the archive.
func OpenCodeOutputContract(schema json.RawMessage) string {
	if len(schema) == 0 {
		return ""
	}
	return "\n\nReturn ONLY a JSON object matching the schema below. No prose before or after it.\n\nSchema:\n" +
		string(schema) + "\n"
}

// openCodeStream is local to one Run. A stop event selects its message's text;
// a later step invalidates that answer until it also completes.
type openCodeStream struct {
	messages     map[string]*openCodeMessage
	steps        map[string]int
	finalMessage string
	tokens       int
	err          error
}

type openCodeMessage struct {
	parts map[string]string
	order []string
}

// text joins parts in first-seen order, using the latest value of each part.
func (m *openCodeMessage) text() string {
	var text strings.Builder
	for _, id := range m.order {
		text.WriteString(m.parts[id])
	}
	return text.String()
}

type openCodeEvent struct {
	Type  string          `json:"type"`
	Part  json.RawMessage `json:"part"`
	Error json.RawMessage `json:"error"`
}

type openCodePart struct {
	ID        string `json:"id"`
	MessageID string `json:"messageID"`
	Text      string `json:"text"`
	Tool      string `json:"tool"`
	Reason    string `json:"reason"`
	State     struct {
		Input json.RawMessage `json:"input"`
	} `json:"state"`
	Tokens *openCodeTokens `json:"tokens"`
}

type openCodeTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

func (s *openCodeStream) line(line string, emit func(Event)) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	var event openCodeEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return fmt.Errorf("invalid JSON event: %w", err)
	}
	switch event.Type {
	case "error":
		return openCodeError(event.Error, emit)
	case "step_start", "step_finish", "text", "reasoning", "tool_use":
		var part openCodePart
		if err := json.Unmarshal(event.Part, &part); err != nil {
			return fmt.Errorf("invalid %s event: %w", event.Type, err)
		}
		if part.MessageID == "" {
			return fmt.Errorf("%s event has no message ID", event.Type)
		}
		return s.part(event.Type, part, emit)
	case "":
		return errors.New("OpenCode event has no type")
	default:
		// New event types remain in the raw archive and touch the byte watchdog.
		return nil
	}
}

func (s *openCodeStream) part(kind string, part openCodePart, emit func(Event)) error {
	switch kind {
	case "step_start":
		s.finalMessage = ""
	case "text":
		if part.ID == "" {
			return errors.New("text event has no part ID")
		}
		message := s.messages[part.MessageID]
		if message == nil {
			message = &openCodeMessage{parts: map[string]string{}}
			s.messages[part.MessageID] = message
		}
		if _, seen := message.parts[part.ID]; !seen {
			message.order = append(message.order, part.ID)
		}
		message.parts[part.ID] = part.Text
		if part.Text != "" {
			emit(Event{Kind: EventActivity, Text: clampRunes(flattenLines(part.Text))})
		}
	case "reasoning":
		// Reasoning summaries are real activity, but never answer text.
		if part.Text != "" {
			emit(Event{Kind: EventActivity, Text: clampRunes(flattenLines(part.Text))})
		}
	case "tool_use":
		if part.Tool == "" {
			return errors.New("tool event has no tool name")
		}
		arg := (contentBlock{Input: part.State.Input}).arg()
		emit(Event{Kind: EventProgress, Text: strings.TrimSpace(part.Tool + " " + arg)})
	case "step_finish":
		return s.finish(part)
	}
	return nil
}

func (s *openCodeStream) finish(part openCodePart) error {
	if part.ID == "" || part.Reason == "" || part.Tokens == nil {
		return errors.New("incomplete step_finish event")
	}
	u := part.Tokens
	if u.Input < 0 || u.Output < 0 || u.Reasoning < 0 || u.Cache.Read < 0 || u.Cache.Write < 0 {
		return errors.New("negative OpenCode token count")
	}
	total := u.Input + u.Output + u.Reasoning + u.Cache.Read + u.Cache.Write
	previous, seen := s.steps[part.ID]
	s.tokens += total - previous
	s.steps[part.ID] = total
	if seen {
		return nil
	}
	s.finalMessage = ""
	if part.Reason == "stop" {
		s.finalMessage = part.MessageID
	}
	return nil
}

func openCodeError(raw json.RawMessage, emit func(Event)) error {
	var diagnostic struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &diagnostic); err != nil || diagnostic.Message == "" {
		return errors.New("invalid OpenCode error event")
	}
	text := clampRunes(flattenLines(diagnostic.Message))
	emit(Event{Kind: EventInfo, Text: text})
	return fmt.Errorf("%s: %s", diagnostic.Type, text)
}

// openCodeJSON accepts fences and prose before the first JSON container.
// An invalid or non-object container must not yield a nested or later answer.
func openCodeJSON(text string) (json.RawMessage, error) {
	start := strings.IndexAny(text, "{[")
	if start < 0 || text[start] != '{' {
		return nil, errors.New("no JSON object in OpenCode answer")
	}
	var out json.RawMessage
	if err := json.NewDecoder(strings.NewReader(text[start:])).Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid JSON object in OpenCode answer: %w", err)
	}
	return out, nil
}
