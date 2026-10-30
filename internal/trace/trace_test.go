package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newRecorder(t *testing.T) (*Recorder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	r, err := New(path, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, path
}

func mustStart(t *testing.T, ctx context.Context, kind string, data any) (context.Context, *Span) {
	t.Helper()
	next, span, err := Start(ctx, kind, data)
	if err != nil {
		t.Fatal(err)
	}
	return next, span
}

func TestDurableBeforeCloseAndStepInheritance(t *testing.T) {
	r, path := newRecorder(t)
	ctx, run := mustStart(t, Context(context.Background(), r), "run", map[string]string{"input": "identity"})
	stepCtx, step := mustStart(t, ctx, "step", nil)
	_, call := mustStart(t, stepCtx, "llm_call", map[string]string{"request": "full payload"})
	if err := call.End(map[string]string{"response": "full response"}, nil); err != nil {
		t.Fatal(err)
	}
	log, err := Read(path)
	if err != nil || log.TruncatedTail || len(log.Events) != 4 {
		t.Fatalf("log=%+v err=%v", log, err)
	}
	if log.Events[0].StepID != "" || log.Events[1].StepID != log.Events[1].SpanID || log.Events[2].StepID != log.Events[1].SpanID || log.Events[3].ParentSpanID != step.id {
		t.Fatalf("incorrect hierarchy: %+v", log.Events)
	}
	if err := step.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := run.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	log, err = Read(path)
	if err != nil || len(log.Events) != 6 {
		t.Fatalf("%+v %v", log, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v: %v", info, err)
	}
	if _, err := New(path, "run-1"); err == nil {
		t.Fatal("reopened existing log")
	}
}

func TestNoRecorderAndClassification(t *testing.T) {
	ctx := context.Background()
	got, span, err := Start(ctx, "tool_call", nil)
	if got != ctx || span != nil || err != nil || span.End(nil, nil) != nil {
		t.Fatal("no-op recording failed")
	}
	for _, test := range []struct {
		err      error
		category string
	}{
		{nil, ""}, {fmt.Errorf("cancel: %w", context.Canceled), CategoryCanceled},
		{context.DeadlineExceeded, CategoryTimeout}, {errors.New("disk"), CategoryInfrastructure},
	} {
		if got := Classify(test.err); got != test.category {
			t.Fatalf("%v: %s", test.err, got)
		}
	}
}

func TestEmptyErrorTextRetainsClassification(t *testing.T) {
	r, path := newRecorder(t)
	_, span := mustStart(t, Context(context.Background(), r), "run", nil)
	if err := span.End(nil, errors.New("")); err != nil {
		t.Fatal(err)
	}
	log, err := Read(path)
	if err != nil || len(log.Events) != 2 || log.Events[1].ErrorCategory != CategoryInfrastructure {
		t.Fatalf("%+v %v", log, err)
	}
}

func TestParentOrderAndDuplicateEnd(t *testing.T) {
	r, path := newRecorder(t)
	ctx, parent := mustStart(t, Context(context.Background(), r), "run", nil)
	_, child := mustStart(t, ctx, "tool_call", nil)
	if err := parent.End(nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("parent end: %v", err)
	}
	if err := child.End(nil, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err := child.End(nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate end: %v", err)
	}
	if err := parent.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Start(ctx, "step", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ended parent accepted: %v", err)
	}
	log, err := Read(path)
	if err != nil || len(log.Events) != 4 || log.Events[2].ErrorCategory != CategoryCanceled {
		t.Fatalf("%+v %v", log, err)
	}
}

func TestContextRebindingDoesNotCrossLogs(t *testing.T) {
	r1, p1 := newRecorder(t)
	r2, p2 := newRecorder(t)
	ctx, parent := mustStart(t, Context(context.Background(), r1), "run", nil)
	_, child := mustStart(t, Context(ctx, r1), "step", nil)
	_, other := mustStart(t, Context(ctx, r2), "run", nil)
	if err := other.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := child.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := parent.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	l1, e1 := Read(p1)
	l2, e2 := Read(p2)
	if e1 != nil || e2 != nil || len(l1.Events) != 4 || len(l2.Events) != 2 || l2.Events[0].ParentSpanID != "" {
		t.Fatalf("%+v %v; %+v %v", l1, e1, l2, e2)
	}
}

func TestPartialTailPreservesConfirmedRecords(t *testing.T) {
	r, path := newRecorder(t)
	_, span := mustStart(t, Context(context.Background(), r), "run", nil)
	if err := span.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"{\"unfinished\":", "{}", "not json"} {
		if err := os.WriteFile(path, append(append([]byte{}, original...), tail...), 0600); err != nil {
			t.Fatal(err)
		}
		log, err := Read(path)
		if err != nil || !log.TruncatedTail || len(log.Events) != 2 {
			t.Fatalf("tail %q: %+v %v", tail, log, err)
		}
	}
	if err := os.WriteFile(path, append(append([]byte{}, original...), "bad\n"...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("complete corruption accepted: %v", err)
	}
}

func TestConcurrentChildrenProduceOrderedChain(t *testing.T) {
	r, path := newRecorder(t)
	ctx, parent := mustStart(t, Context(context.Background(), r), "run", nil)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, child, err := Start(ctx, "tool_call", i)
			if err == nil {
				err = child.End(i, nil)
			}
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := parent.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	log, err := Read(path)
	if err != nil || len(log.Events) != 130 {
		t.Fatalf("events=%d err=%v", len(log.Events), err)
	}
}

func TestStickyPersistenceFaultsStopFurtherWork(t *testing.T) {
	for _, mode := range []string{"write", "short", "sync"} {
		t.Run(mode, func(t *testing.T) {
			r, path := newRecorder(t)
			ctx := Context(context.Background(), r)
			originalWrite, originalSync := r.write, r.sync
			sentinel := errors.New("injected " + mode)
			switch mode {
			case "write":
				r.write = func([]byte) (int, error) { return 0, sentinel }
			case "short":
				r.write = func(p []byte) (int, error) { return len(p) - 1, nil }
			case "sync":
				r.sync = func() error { return sentinel }
			}
			_, _, first := Start(ctx, "run", nil)
			if !errors.Is(first, ErrPersistence) {
				t.Fatalf("%v", first)
			}
			r.write, r.sync = originalWrite, originalSync
			_, _, second := Start(ctx, "run", nil)
			if second != first {
				t.Fatalf("fault not sticky: %v / %v", first, second)
			}
			if err := r.Close(); !errors.Is(err, ErrPersistence) {
				t.Fatalf("close lost fault: %v", err)
			}
			_, _ = Read(path) // A sync failure may leave readable bytes, never an acknowledged span.
		})
	}
}

func TestEndFailureAndClosePreventContinuation(t *testing.T) {
	r, _ := newRecorder(t)
	ctx, span := mustStart(t, Context(context.Background(), r), "run", nil)
	r.sync = func() error { return errors.New("disk unavailable") }
	err := span.End(nil, nil)
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("%v", err)
	}
	if _, _, next := Start(ctx, "step", nil); next != err {
		t.Fatalf("continuation after failed end: %v", next)
	}
	other, _ := newRecorder(t)
	otherCtx := Context(context.Background(), other)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Start(otherCtx, "run", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("%v", err)
	}
}

func TestReadRejectsTamperingAndInvalidPairing(t *testing.T) {
	r, path := newRecorder(t)
	ctx, parent := mustStart(t, Context(context.Background(), r), "run", nil)
	_, child := mustStart(t, ctx, "step", nil)
	if err := child.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := parent.End(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func([]Event)
		rehash bool
	}{
		{"payload", func(e []Event) { e[0].Data = json.RawMessage(`{"changed":true}`) }, false},
		{"sequence", func(e []Event) { e[2].Sequence++ }, true},
		{"schema", func(e []Event) { e[0].SchemaVersion++ }, true},
		{"run identity", func(e []Event) { e[2].RunID = "other" }, true},
		{"step", func(e []Event) { e[2].StepID = "other" }, true},
		{"parent", func(e []Event) { e[1].ParentSpanID = "missing" }, true},
		{"end active parent", func(e []Event) {
			e[2].SpanID = e[0].SpanID
			e[2].ParentSpanID = ""
			e[2].StepID = ""
			e[2].Kind = "run"
		}, true},
		{"unpaired end", func(e []Event) { e[0].Phase = "end" }, true},
		{"bad category", func(e []Event) { e[3].Error = "bad"; e[3].ErrorCategory = "made_up" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := append([]Event(nil), log.Events...)
			test.change(events)
			var raw bytes.Buffer
			previous := ""
			for _, event := range events {
				if test.rehash {
					event.PreviousSHA256 = previous
					event.SHA256, _ = eventDigest(event)
				}
				data, _ := json.Marshal(event)
				raw.Write(data)
				raw.WriteByte('\n')
				previous = event.SHA256
			}
			if err := os.WriteFile(path, raw.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(path); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestIncompleteLogAndSizeLimits(t *testing.T) {
	r, path := newRecorder(t)
	_, _ = mustStart(t, Context(context.Background(), r), "run", nil)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := Read(path)
	if err != nil || len(log.Events) != 1 || log.Events[0].Phase != "begin" || log.TruncatedTail {
		t.Fatalf("%+v %v", log, err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, MaxLineBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize accepted: %v", err)
	}
	other, _ := newRecorder(t)
	if _, _, err := Start(Context(context.Background(), other), "run", string(bytes.Repeat([]byte{'x'}, MaxLineBytes))); !errors.Is(err, ErrPersistence) {
		t.Fatalf("oversize written: %v", err)
	}
}
