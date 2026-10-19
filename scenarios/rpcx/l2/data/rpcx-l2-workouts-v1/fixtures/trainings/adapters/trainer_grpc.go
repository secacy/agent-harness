package adapters

import (
	"context"
	"example.com/rpcx"
	"time"
)

// TrainerGrpc 保留存量类型名称；仅保留取消路径的出站操作。
type TrainerGrpc struct{ client *rpcx.Client }

func NewTrainerGrpc(addr string) (TrainerGrpc, error) {
	client, err := rpcx.NewClient(addr)
	return TrainerGrpc{client: client}, err
}

// CancelTraining 保留原训练时刻，v1 的 Call 不接受 ctx。
func (s TrainerGrpc) CancelTraining(ctx context.Context, trainingTime time.Time) error {
	return s.client.Call("cancel_training", CancelTrainingRequest{Time: trainingTime}, &struct{}{})
}

// CancelTrainingRequest 用 Go 时刻表示原 protobuf 时间戳的同一时刻。
type CancelTrainingRequest struct{ Time time.Time }
