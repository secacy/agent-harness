package decorator_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/common/decorator"
	"github.com/sirupsen/logrus"
)

type sample struct{}
type handlerFunc func(context.Context, sample) error

func (f handlerFunc) Handle(ctx context.Context, cmd sample) error { return f(ctx, cmd) }

type recordedMetrics map[string]int

func (m recordedMetrics) Inc(key string, value int) { m[key] += value }

func TestCommandDecorators(t *testing.T) {
	for _, mode := range []string{"success", "error"} {
		t.Run(mode, func(t *testing.T) {
			type key struct{}
			ctx := context.WithValue(context.Background(), key{}, "request")
			metrics := recordedMetrics{}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			sentinel := errors.New("handler failure")
			var expected error
			if mode == "error" {
				expected = sentinel
			}
			calls := 0
			handler := decorator.ApplyCommandDecorators[sample](handlerFunc(func(received context.Context, _ sample) error {
				calls++
				if received != ctx || received.Value(key{}) != "request" {
					t.Error("decorators replaced request context")
				}
				return expected
			}), logrus.NewEntry(logger), metrics)
			if err := handler.Handle(ctx, sample{}); !errors.Is(err, expected) || calls != 1 {
				t.Errorf("handler calls=%d error=%v", calls, err)
			}
			outcome := "success"
			if mode == "error" {
				outcome = "failure"
			}
			if metrics["commands.sample."+outcome] != 1 || len(metrics) != 2 {
				t.Errorf("metrics=%v", metrics)
			}
		})
	}
}
