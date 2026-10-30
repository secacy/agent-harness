package trace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func directory(path string) string { return filepath.Dir(path) }

// Read verifies every complete record and ignores only a final unterminated
// line. Open spans are permitted: callers must not mistake readability for run
// completion. A full malformed line is corruption even at the end of a file.
func Read(path string) (Log, error) {
	f, err := os.Open(path)
	if err != nil {
		return Log{}, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	log := Log{Events: []Event{}}
	states := map[string]*spanState{}
	runID, previous := "", ""
	for {
		var line []byte
		for {
			part, readErr := reader.ReadSlice('\n')
			if len(line)+len(part) > MaxLineBytes {
				return log, fmt.Errorf("%w: line exceeds 16 MiB", ErrInvalid)
			}
			line = append(line, part...)
			if errors.Is(readErr, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(readErr, io.EOF) {
				log.TruncatedTail = len(line) != 0
				return log, nil
			}
			if readErr != nil {
				return log, readErr
			}
			break
		}
		var event Event
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			return log, fmt.Errorf("%w: line %d: %v", ErrInvalid, len(log.Events)+1, err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return log, fmt.Errorf("%w: trailing content", ErrInvalid)
		}
		// Writer output is canonical. This also rejects duplicate top-level keys,
		// whitespace edits and ambiguous encodings before trusting the hash chain.
		canonical, err := json.Marshal(event)
		if err != nil || !bytes.Equal(canonical, bytes.TrimSuffix(line, []byte{'\n'})) {
			return log, fmt.Errorf("%w: noncanonical record", ErrInvalid)
		}
		if event.SchemaVersion != SchemaVersion || event.Sequence != int64(len(log.Events))+1 ||
			strings.TrimSpace(event.RunID) == "" || strings.TrimSpace(event.Kind) == "" || event.At.IsZero() || event.SpanID == "" || len(event.Data) == 0 {
			return log, fmt.Errorf("%w: schema, identity or sequence", ErrInvalid)
		}
		if runID == "" {
			runID = event.RunID
		}
		digest, digestErr := eventDigest(event)
		if digestErr != nil || event.RunID != runID || event.PreviousSHA256 != previous || event.SHA256 != digest {
			return log, fmt.Errorf("%w: record chain mismatch", ErrInvalid)
		}
		if (event.Error != "" && event.ErrorCategory == "") ||
			!validCategory(event.ErrorCategory) {
			return log, fmt.Errorf("%w: error classification", ErrInvalid)
		}
		expectedStatus := "running"
		if event.Phase == "end" {
			expectedStatus = "ok"
			if event.ErrorCategory != "" {
				expectedStatus = "error"
			}
		}
		if event.Status != expectedStatus {
			return log, fmt.Errorf("%w: span status mismatch", ErrInvalid)
		}
		switch event.Phase {
		case "begin":
			if states[event.SpanID] != nil || event.Error != "" || event.ErrorCategory != "" {
				return log, fmt.Errorf("%w: duplicate or failed begin", ErrInvalid)
			}
			step := ""
			if event.ParentSpanID != "" {
				parent := states[event.ParentSpanID]
				if parent == nil || parent.ended {
					return log, fmt.Errorf("%w: unavailable parent", ErrInvalid)
				}
				step = parent.step
				parent.children++
			}
			if event.Kind == "step" {
				step = event.SpanID
			}
			if event.StepID != step {
				return log, fmt.Errorf("%w: step inheritance mismatch", ErrInvalid)
			}
			states[event.SpanID] = &spanState{parent: event.ParentSpanID, step: step, kind: event.Kind}
		case "end":
			state := states[event.SpanID]
			if state == nil || state.ended || state.children != 0 || state.kind != event.Kind || state.parent != event.ParentSpanID || state.step != event.StepID {
				return log, fmt.Errorf("%w: span end mismatch", ErrInvalid)
			}
			state.ended = true
			if state.parent != "" {
				states[state.parent].children--
			}
		default:
			return log, fmt.Errorf("%w: unsupported phase", ErrInvalid)
		}
		log.Events = append(log.Events, event)
		previous = event.SHA256
	}
}
