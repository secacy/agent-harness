package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/secacy/agent-harness/internal/trace"
)

// Failure preserves the error identities used by Loop, separately from its text.
// Provider-specific errors remain text; no provider credentials are recorded.
type Failure struct {
	Message string
	Kinds   []string
}

func failure(err error) *Failure {
	if err == nil {
		return nil
	}
	f := &Failure{Message: err.Error()}
	for _, k := range errorKinds {
		if errors.Is(err, k.err) {
			f.Kinds = append(f.Kinds, k.name)
		}
	}
	return f
}

var errorKinds = []struct {
	name string
	err  error
}{
	{"budget", ErrModelBudget}, {"protocol", ErrProtocol}, {"truncated", ErrOutputTruncated},
	{"invalid_input", ErrInvalidInput}, {"cancelled", context.Canceled}, {"timeout", context.DeadlineExceeded},
}

func (f *Failure) Error() string { return f.Message }
func (f *Failure) Is(err error) bool {
	for _, k := range errorKinds {
		if k.err == err {
			for _, name := range f.Kinds {
				if name == k.name {
					return true
				}
			}
		}
	}
	return false
}
func (f *Failure) asError() error {
	if f == nil {
		return nil
	}
	return f
}
func contextFailure(ctx context.Context) string { return contextErrorKind(ctx.Err()) }
func contextErrorKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if err != nil {
		return "cancelled"
	}
	return ""
}

type LoopInput struct {
	ModelName        string
	Initial          []Message
	Tools            []ToolSpec
	Limits           Limits
	CompletionPolicy CompletionPolicy
}
type LoopOutput struct {
	Result       Result
	Failure      *Failure
	ContextError string
}
type ModelInput struct {
	ModelName string
	Messages  []Message
	Tools     []ToolSpec
}
type ModelOutput struct {
	// Cost is unknown unless a separately frozen price source is available.
	Cost         *float64
	Reply        Reply
	Failure      *Failure
	ContextError string
}
type ToolInput struct {
	Call     ToolCall
	Executed bool
}
type ToolOutput struct {
	Result       ToolResult
	Failure      *Failure
	ContextError string
}
type RecordedModel struct {
	Input  ModelInput
	Output ModelOutput
}
type RecordedTool struct {
	Input  ToolInput
	Output ToolOutput
}
type RecordedBoundary struct {
	Name         string
	ContextError string
}

type Operation struct {
	Boundary *RecordedBoundary
	Model    *RecordedModel
	Tool     *RecordedTool
}
type Recording struct {
	Input      LoopInput
	Output     LoopOutput
	Operations []Operation
}

// RecordingFromTrace reads complete Loop inputs and operations; open spans never
// become successful operations. The caller still verifies the enclosing Run.
func RecordingFromTrace(log trace.Log) (Recording, error) {
	var r Recording
	ends := map[string]trace.Event{}
	var loops []trace.Event
	for _, e := range log.Events {
		if e.Phase == "end" {
			ends[e.SpanID] = e
		}
		if e.Kind == "agent" && e.Phase == "begin" {
			loops = append(loops, e)
		}
	}
	if log.TruncatedTail || len(loops) != 1 {
		return r, errors.New("trace needs one complete agent loop")
	}
	loop := loops[0]
	end, ok := ends[loop.SpanID]
	if !ok {
		return r, errors.New("agent loop is incomplete")
	}
	if err := json.Unmarshal(loop.Data, &r.Input); err != nil {
		return r, err
	}
	if err := json.Unmarshal(end.Data, &r.Output); err != nil {
		return r, err
	}
	for _, e := range log.Events {
		if e.Phase != "begin" || (e.Kind != "llm_call" && e.Kind != "tool_call" && e.Kind != "agent_boundary") {
			continue
		}
		out, ok := ends[e.SpanID]
		if !ok {
			return r, fmt.Errorf("incomplete %s", e.Kind)
		}
		if e.Kind == "agent_boundary" {
			v := &RecordedBoundary{}
			if err := json.Unmarshal(e.Data, v); err != nil {
				return r, err
			}
			var output RecordedBoundary
			if err := json.Unmarshal(out.Data, &output); err != nil {
				return r, err
			}
			v.ContextError = output.ContextError
			r.Operations = append(r.Operations, Operation{Boundary: v})
		} else if e.Kind == "llm_call" {
			v := &RecordedModel{}
			if err := json.Unmarshal(e.Data, &v.Input); err != nil {
				return r, err
			}
			if err := json.Unmarshal(out.Data, &v.Output); err != nil {
				return r, err
			}
			r.Operations = append(r.Operations, Operation{Model: v})
		} else {
			v := &RecordedTool{}
			if err := json.Unmarshal(e.Data, &v.Input); err != nil {
				return r, err
			}
			if err := json.Unmarshal(out.Data, &v.Output); err != nil {
				return r, err
			}
			r.Operations = append(r.Operations, Operation{Tool: v})
		}
	}
	return r, nil
}

type replayStopKey struct{}

type replayContext struct {
	context.Context
	cause error
}

func (c *replayContext) Err() error {
	if e := c.Context.Err(); e != nil {
		return e
	}
	return c.cause
}
func (c *replayContext) inject(kind string) {
	if kind == "timeout" {
		c.cause = context.DeadlineExceeded
	}
	if kind == "cancelled" {
		c.cause = context.Canceled
	}
}

type playback struct {
	recording Recording
	index     int
	ctx       *replayContext
	fault     error
	allowed   map[string]bool
}

func (p *playback) unknown(call ToolCall, output ToolResult) error {
	if p.index >= len(p.recording.Operations) || p.recording.Operations[p.index].Tool == nil {
		p.fault = errors.New("missing recorded unknown tool")
		return p.fault
	}
	v := p.recording.Operations[p.index].Tool
	p.index++
	if v.Input.Executed || !reflect.DeepEqual(v.Input.Call, call) || !reflect.DeepEqual(v.Output.Result, output) || v.Output.Failure != nil {
		p.fault = errors.New("unknown tool call/result differs from recording")
		return p.fault
	}
	p.ctx.inject(v.Output.ContextError)
	return nil
}
func (p *playback) boundary(name string) error {
	if p.index >= len(p.recording.Operations) || p.recording.Operations[p.index].Boundary == nil {
		p.fault = errors.New("missing recorded control boundary")
		return p.fault
	}
	v := p.recording.Operations[p.index].Boundary
	p.index++
	if v.Name != name {
		p.fault = errors.New("control boundary differs from recording")
		return p.fault
	}
	p.ctx.inject(v.ContextError)
	return nil
}
func (l Loop) checkContext(ctx context.Context, name string) error {
	if l.replayBoundary != nil {
		if err := l.replayBoundary(name); err != nil {
			return err
		}
	}
	_, span, err := trace.Start(ctx, "agent_boundary", RecordedBoundary{Name: name})
	if err != nil {
		return err
	}
	contextErr := ctx.Err()
	return errors.Join(contextErr, span.End(RecordedBoundary{Name: name, ContextError: contextErrorKind(contextErr)}, contextErr))
}
func (p *playback) Complete(_ context.Context, messages []Message, tools []ToolSpec) (Reply, error) {
	if p.fault != nil {
		return Reply{}, p.fault
	}
	if p.index >= len(p.recording.Operations) || p.recording.Operations[p.index].Model == nil {
		p.fault = errors.New("unexpected model request during replay")
		return Reply{}, p.fault
	}
	v := p.recording.Operations[p.index].Model
	p.index++
	if !reflect.DeepEqual(ModelInput{Messages: messages, Tools: tools, ModelName: p.recording.Input.ModelName}, v.Input) {
		p.fault = fmt.Errorf("model request %d differs from recording", p.index)
		return Reply{}, p.fault
	}
	p.ctx.inject(v.Output.ContextError)
	return v.Output.Reply, v.Output.Failure.asError()
}
func (p *playback) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	if p.fault != nil {
		return ToolResult{}, p.fault
	}
	if p.index >= len(p.recording.Operations) || p.recording.Operations[p.index].Tool == nil {
		p.fault = errors.New("unexpected tool call during replay")
		return ToolResult{}, p.fault
	}
	v := p.recording.Operations[p.index].Tool
	p.index++
	if !v.Input.Executed || !reflect.DeepEqual(call, v.Input.Call) {
		p.fault = errors.New("tool call differs from recording")
		return ToolResult{}, p.fault
	}
	p.ctx.inject(v.Output.ContextError)
	return v.Output.Result, v.Output.Failure.asError()
}

// ReplayRecording drives the production Loop with recorded model and tool
// responses. It neither calls a provider nor executes repository tools. A nil
// error means the recorded outcome (which itself may be failure) was reproduced.
func ReplayRecording(ctx context.Context, r Recording) (Result, error) {
	c := &replayContext{Context: trace.Context(ctx, nil)}
	p := &playback{recording: r, ctx: c, allowed: map[string]bool{}}
	for _, t := range r.Input.Tools {
		p.allowed[t.Name] = true
	}
	if len(r.Operations) == 0 {
		c.inject(r.Output.ContextError)
	}
	l := Loop{replayUnknown: p.unknown, ModelName: r.Input.ModelName, CompletionPolicy: r.Input.CompletionPolicy, Model: p, Executor: p, Tools: r.Input.Tools, Limits: r.Input.Limits}
	for _, op := range r.Operations {
		if op.Boundary != nil {
			l.replayBoundary = p.boundary
			break
		}
	}
	runCtx := context.Context(c)
	if l.replayBoundary != nil {
		runCtx = context.WithValue(c, replayStopKey{}, p.boundary)
	}
	result, runErr := l.Run(runCtx, r.Input.Initial)
	if p.fault != nil {
		return result, p.fault
	}
	if p.index != len(r.Operations) {
		return result, errors.New("replay left unused operations")
	}
	if !reflect.DeepEqual(result, r.Output.Result) {
		return result, errors.New("replayed agent outcome differs from recording")
	}
	actual := failure(runErr)
	if !reflect.DeepEqual(actual, r.Output.Failure) {
		return result, fmt.Errorf("replayed error differs: %v", runErr)
	}
	return result, nil
}

func toolTraceError(err error) error {
	if errors.Is(err, trace.ErrPersistence) || errors.Is(err, trace.ErrClosed) {
		return err
	}
	return trace.Categorize(err, "tool")
}
func modelTraceError(err error) error {
	var categorized interface{ TraceErrorCategory() string }
	if errors.As(err, &categorized) {
		return err
	}
	if errors.Is(err, ErrModelBudget) {
		return trace.Categorize(err, "budget")
	}
	if errors.Is(err, ErrProtocol) || errors.Is(err, ErrOutputTruncated) || errors.Is(err, ErrInvalidInput) {
		return trace.Categorize(err, "protocol")
	}
	if errors.Is(err, trace.ErrPersistence) || errors.Is(err, trace.ErrClosed) {
		return err
	}
	return trace.Categorize(err, "model")
}
