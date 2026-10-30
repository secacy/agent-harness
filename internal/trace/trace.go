// Package trace records durable, ordered spans without depending on runtime or
// scenario types. A readable log may be incomplete; callers must separately
// establish that a run and all required spans have completed.
package trace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	SchemaVersion          = 1
	MaxLineBytes           = 16 * 1024 * 1024 // Includes the terminating newline.
	CategoryCanceled       = "canceled"
	CategoryTimeout        = "timeout"
	CategoryInfrastructure = "infrastructure"
)

var (
	ErrInvalid     = errors.New("invalid trace")
	ErrClosed      = errors.New("trace recorder is closed")
	ErrPersistence = errors.New("persist trace")
)

type Event struct {
	SchemaVersion  int             `json:"schema_version"`
	Sequence       int64           `json:"sequence"`
	RunID          string          `json:"run_id"`
	SpanID         string          `json:"span_id"`
	ParentSpanID   string          `json:"parent_span_id,omitempty"`
	StepID         string          `json:"step_id,omitempty"`
	Kind           string          `json:"kind"`
	Phase          string          `json:"phase"`
	Status         string          `json:"status"`
	At             time.Time       `json:"at"`
	Data           json.RawMessage `json:"data"`
	Error          string          `json:"error,omitempty"`
	ErrorCategory  string          `json:"error_category,omitempty"`
	PreviousSHA256 string          `json:"previous_sha256,omitempty"`
	SHA256         string          `json:"sha256"`
}

type Log struct {
	Events        []Event
	TruncatedTail bool
}

type spanState struct {
	parent, step, kind string
	children           int
	ended              bool
}

// Recorder owns one exclusively created JSONL file. A persistence failure is
// sticky: the caller cannot proceed past a record whose durability is unknown.
type Recorder struct {
	mu       sync.Mutex
	file     *os.File
	runID    string
	sequence int64
	previous string
	spans    map[string]*spanState
	closed   bool
	fault    error
	write    func([]byte) (int, error)
	sync     func() error
}

type Span struct {
	recorder *Recorder
	id       string
}

type contextKey struct{}
type binding struct {
	recorder     *Recorder
	parent, step string
}

// New never reopens an existing file, including an interrupted trace. The file
// and its directory entry are durable before a recorder is returned.
func New(path, runID string) (*Recorder, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("%w: run ID is required", ErrInvalid)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("%w: create: %w", ErrPersistence, err)
	}
	fail := func(err error) (*Recorder, error) {
		return nil, errors.Join(fmt.Errorf("%w: create: %w", ErrPersistence, err), f.Close())
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	dir, err := os.Open(directory(path))
	if err != nil {
		return fail(err)
	}
	if err := errors.Join(dir.Sync(), dir.Close()); err != nil {
		return fail(err)
	}
	return &Recorder{file: f, runID: runID, spans: map[string]*spanState{}, write: f.Write, sync: f.Sync}, nil
}

// Context binds a recorder. Rebinding the same recorder preserves its current
// parent span; changing recorders starts a new root rather than crossing logs.
func Context(ctx context.Context, recorder *Recorder) context.Context {
	if prior, ok := ctx.Value(contextKey{}).(binding); ok && prior.recorder == recorder {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, binding{recorder: recorder})
}

// Start durably publishes the begin record before work is allowed to start.
// Without a recorder, the returned nil span is safe to End.
func Start(ctx context.Context, kind string, data any) (context.Context, *Span, error) {
	b, _ := ctx.Value(contextKey{}).(binding)
	if b.recorder == nil {
		return ctx, nil, nil
	}
	r := b.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.available(); err != nil {
		return ctx, nil, err
	}
	if strings.TrimSpace(kind) == "" {
		return ctx, nil, fmt.Errorf("%w: empty span kind", ErrInvalid)
	}
	if b.parent != "" {
		parent := r.spans[b.parent]
		if parent == nil || parent.ended || parent.step != b.step {
			return ctx, nil, fmt.Errorf("%w: parent is unavailable", ErrInvalid)
		}
	}
	id := fmt.Sprintf("span-%d", r.sequence+1)
	step := b.step
	if kind == "step" {
		step = id
	}
	event := Event{SpanID: id, ParentSpanID: b.parent, StepID: step, Kind: kind, Phase: "begin"}
	if err := r.append(event, data, nil); err != nil {
		return ctx, nil, err
	}
	r.spans[id] = &spanState{parent: b.parent, step: step, kind: kind}
	if b.parent != "" {
		r.spans[b.parent].children++
	}
	span := &Span{recorder: r, id: id}
	return context.WithValue(ctx, contextKey{}, binding{recorder: r, parent: id, step: step}), span, nil
}

// End durably publishes a span's outcome. A parent cannot finish while a child
// is active. Context cancellation does not suppress evidence of completed work.
func (s *Span) End(data any, cause error) error {
	if s == nil {
		return nil
	}
	if s.recorder == nil {
		return fmt.Errorf("%w: span has no recorder", ErrInvalid)
	}
	r := s.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.available(); err != nil {
		return err
	}
	state := r.spans[s.id]
	if state == nil || state.ended {
		return fmt.Errorf("%w: span already ended or unknown", ErrInvalid)
	}
	if state.children != 0 {
		return fmt.Errorf("%w: span has active children", ErrInvalid)
	}
	event := Event{SpanID: s.id, ParentSpanID: state.parent, StepID: state.step, Kind: state.kind, Phase: "end"}
	if err := r.append(event, data, cause); err != nil {
		return err
	}
	state.ended = true
	if state.parent != "" {
		r.spans[state.parent].children--
	}
	return nil
}

func (r *Recorder) available() error {
	if r.fault != nil {
		return r.fault
	}
	if r.closed {
		return ErrClosed
	}
	return nil
}

func (r *Recorder) append(event Event, data any, cause error) error {
	fail := func(err error) error {
		r.fault = fmt.Errorf("%w: %w", ErrPersistence, err)
		return r.fault
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fail(err)
	}
	event.SchemaVersion, event.Sequence, event.RunID = SchemaVersion, r.sequence+1, r.runID
	event.At, event.Data, event.PreviousSHA256 = time.Now().UTC(), payload, r.previous
	event.Status = "running"
	if event.Phase == "end" {
		event.Status = "ok"
		if cause != nil {
			event.Status = "error"
		}
	}
	if cause != nil {
		event.Error, event.ErrorCategory = cause.Error(), Classify(cause)
	}
	event.SHA256, err = eventDigest(event)
	if err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return fail(err)
	}
	if len(raw)+1 > MaxLineBytes {
		return fail(errors.New("event exceeds 16 MiB line limit"))
	}
	raw = append(raw, '\n')
	n, err := r.write(raw)
	if err != nil {
		return fail(err)
	}
	if n != len(raw) {
		return fail(io.ErrShortWrite)
	}
	if err := r.sync(); err != nil {
		return fail(err)
	}
	r.sequence, r.previous = event.Sequence, event.SHA256
	return nil
}

// Close does not manufacture end records for active spans. It remains safe to
// inspect a closed, incomplete log and to distinguish it from a completed run.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.fault
	}
	r.closed = true
	err := r.file.Close()
	if err != nil && r.fault == nil {
		r.fault = fmt.Errorf("%w: close: %w", ErrPersistence, err)
	}
	return errors.Join(r.fault, err)
}

type classifiedError struct {
	error
	category string
}

func (e classifiedError) Unwrap() error              { return e.error }
func (e classifiedError) TraceErrorCategory() string { return e.category }

// Categorize preserves error identity while attaching a runtime failure class.
func Categorize(err error, category string) error {
	if err == nil {
		return nil
	}
	return classifiedError{err, category}
}
func validCategory(category string) bool {
	switch category {
	case "", CategoryCanceled, CategoryTimeout, CategoryInfrastructure, "budget", "model", "protocol", "tool", "compile", "test", "gate", "input":
		return true
	}
	return false
}

// Classify recognizes error identity first, then explicit domain classifications.
func Classify(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return CategoryCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CategoryTimeout
	}
	var classified interface{ TraceErrorCategory() string }
	if errors.As(err, &classified) && validCategory(classified.TraceErrorCategory()) && classified.TraceErrorCategory() != "" {
		return classified.TraceErrorCategory()
	}
	return CategoryInfrastructure
}

func eventDigest(event Event) (string, error) {
	event.SHA256 = ""
	raw, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
