package command

import (
	"context"
	"time"
)

type UserService interface {
	UpdateTrainingBalance(ctx context.Context, userID string, amountChange int) error
}

// TrainerService 仅保留本切片的取消训练操作。
type TrainerService interface {
	CancelTraining(ctx context.Context, trainingTime time.Time) error
}
