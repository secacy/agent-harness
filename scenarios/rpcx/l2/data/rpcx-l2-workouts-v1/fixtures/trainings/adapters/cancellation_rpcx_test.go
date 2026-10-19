package adapters_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/rpcx"
	"github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/common/metrics"
	"github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/trainings/adapters"
	"github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/trainings/app/command"
	"github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/trainings/domain/training"
	"github.com/sirupsen/logrus"
)

// callbackRepository 对副本执行真实回调，仅在成功后提交；不模拟远端事务回滚。
type callbackRepository struct {
	value  training.Training
	events *[]string
	err    error
}

func (*callbackRepository) AddTraining(context.Context, *training.Training) error {
	panic("unexpected AddTraining")
}
func (*callbackRepository) GetTraining(context.Context, string, training.User) (*training.Training, error) {
	panic("unexpected GetTraining")
}
func (r *callbackRepository) UpdateTraining(ctx context.Context, id string, user training.User, update func(context.Context, *training.Training) (*training.Training, error)) error {
	*r.events = append(*r.events, "load")
	if r.err != nil {
		return r.err
	}
	if id != r.value.UUID() || user.UUID() != "requester" {
		return errors.New("unexpected repository arguments")
	}
	copy := r.value
	result, err := update(ctx, &copy)
	if err != nil {
		return err
	}
	r.value = *result
	*r.events = append(*r.events, "commit")
	return nil
}

func TestCancellationThroughRPC(t *testing.T) {
	remote := errors.New("remote failure")
	for _, tc := range []struct {
		name                    string
		role                    training.UserType
		hours                   int
		delta                   int64
		userError, trainerError bool
		expected                []string
	}{
		{name: "attendee-free", role: training.Attendee, hours: 48, delta: 1, expected: []string{"load", "balance", "trainer", "commit"}},
		{name: "trainer-free", role: training.Trainer, hours: 48, delta: 1, expected: []string{"load", "balance", "trainer", "commit"}},
		{name: "trainer-penalty", role: training.Trainer, hours: 12, delta: 2, expected: []string{"load", "balance", "trainer", "commit"}},
		{name: "attendee-no-balance", role: training.Attendee, hours: 12, delta: 0, expected: []string{"load", "trainer", "commit"}},
		{name: "balance-error", role: training.Attendee, hours: 48, delta: 1, userError: true, expected: []string{"load", "balance"}},
		{name: "trainer-error", role: training.Attendee, hours: 48, delta: 1, trainerError: true, expected: []string{"load", "balance", "trainer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			trainingTime := time.Now().Add(time.Duration(tc.hours) * time.Hour)
			tr, err := training.NewTraining("training-1", "owner", "Owner", trainingTime)
			if err != nil {
				t.Fatal(err)
			}
			repo := &callbackRepository{value: *tr, events: &events}
			var observed []context.Context
			var balances []adapters.UpdateTrainingBalanceRequest
			t.Cleanup(rpcx.Register(t.Name()+"/users", func(ctx context.Context, method string, request, response any) error {
				events = append(events, "balance")
				observed = append(observed, ctx)
				want := adapters.UpdateTrainingBalanceRequest{UserID: "owner", AmountChange: tc.delta}
				if method != "update_training_balance" || request != want {
					t.Errorf("balance request=%+v method=%s", request, method)
				}
				if _, ok := response.(*struct{}); !ok {
					t.Error("unexpected balance response")
				}
				if tc.userError {
					return remote
				}
				balances = append(balances, want)
				return nil
			}))
			t.Cleanup(rpcx.Register(t.Name()+"/trainer", func(ctx context.Context, method string, request, response any) error {
				events = append(events, "trainer")
				observed = append(observed, ctx)
				req, ok := request.(adapters.CancelTrainingRequest)
				if !ok || method != "cancel_training" || !req.Time.Equal(trainingTime) {
					t.Errorf("trainer request=%+v method=%s", request, method)
				}
				if _, ok := response.(*struct{}); !ok {
					t.Error("unexpected trainer response")
				}
				if tc.trainerError {
					return remote
				}
				return nil
			}))
			users, err := adapters.NewUsersGrpc(t.Name() + "/users")
			if err != nil {
				t.Fatal(err)
			}
			trainer, err := adapters.NewTrainerGrpc(t.Name() + "/trainer")
			if err != nil {
				t.Fatal(err)
			}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			handler := command.NewCancelTrainingHandler(repo, users, trainer, logrus.NewEntry(logger), metrics.NoOp{})
			started := time.Now()
			err = handler.Handle(context.Background(), command.CancelTraining{TrainingUUID: "training-1", User: training.MustNewUser("requester", tc.role)})
			if tc.userError || tc.trainerError {
				prefix := "unable to cancel training"
				if tc.userError {
					prefix = "unable to change trainings balance"
				}
				if !errors.Is(err, remote) || !strings.Contains(err.Error(), prefix) || repo.value.IsCanceled() {
					t.Fatalf("failure committed or lost error chain: %v", err)
				}
			} else if err != nil || !repo.value.IsCanceled() {
				t.Fatalf("cancellation not committed: %v", err)
			}
			if !reflect.DeepEqual(events, tc.expected) {
				t.Errorf("events=%v want=%v", events, tc.expected)
			}
			if tc.trainerError && len(balances) != 1 {
				t.Error("successful remote balance effect was hidden")
			}
			for _, ctx := range observed {
				deadline, ok := ctx.Deadline()
				if !ok || deadline.Before(started.Add(30*time.Second)) || deadline.After(time.Now().Add(30*time.Second)) {
					t.Error("RPC missing per-call 30 second deadline")
				}
				if ctx.Err() == nil {
					t.Error("RPC context not released on return")
				}
			}
		})
	}
}

func TestCancellationPreconditions(t *testing.T) {
	for _, mode := range []string{"already-cancelled", "repository-error"} {
		t.Run(mode, func(t *testing.T) {
			tr, err := training.NewTraining("training-1", "owner", "Owner", time.Now().Add(48*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			expected := training.ErrTrainingAlreadyCanceled
			if mode == "already-cancelled" {
				if err = tr.Cancel(); err != nil {
					t.Fatal(err)
				}
			}
			events := []string{}
			repo := &callbackRepository{value: *tr, events: &events}
			if mode == "repository-error" {
				expected = errors.New("repository failure")
				repo.err = expected
			}
			t.Cleanup(rpcx.Register(t.Name(), func(context.Context, string, any, any) error { t.Error("precondition failure reached RPC"); return nil }))
			users, err := adapters.NewUsersGrpc(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			trainer, err := adapters.NewTrainerGrpc(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			handler := command.NewCancelTrainingHandler(repo, users, trainer, logrus.NewEntry(logger), metrics.NoOp{})
			err = handler.Handle(context.Background(), command.CancelTraining{TrainingUUID: "training-1", User: training.MustNewUser("requester", training.Attendee)})
			if !errors.Is(err, expected) || !reflect.DeepEqual(events, []string{"load"}) {
				t.Errorf("precondition behavior changed: %v %v", err, events)
			}
		})
	}
}

func TestAdapterConnections(t *testing.T) {
	if _, err := adapters.NewUsersGrpc(t.Name() + "/missing"); err == nil {
		t.Error("missing users backend accepted")
	}
	if _, err := adapters.NewTrainerGrpc(t.Name() + "/missing"); err == nil {
		t.Error("missing trainer backend accepted")
	}
}
