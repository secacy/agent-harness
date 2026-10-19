package adapters

import (
	"context"
	"example.com/rpcx"
)

// UsersGrpc 保留存量类型名称；本切片将出站传输替换为 rpcx-v1。
type UsersGrpc struct{ client *rpcx.Client }

func NewUsersGrpc(addr string) (UsersGrpc, error) {
	client, err := rpcx.NewClient(addr)
	return UsersGrpc{client: client}, err
}

// UpdateTrainingBalance 保留原用户与余额映射，v1 的 Call 不接受 ctx。
func (s UsersGrpc) UpdateTrainingBalance(ctx context.Context, userID string, amountChange int) error {
	return s.client.Call("update_training_balance", UpdateTrainingBalanceRequest{UserID: userID, AmountChange: int64(amountChange)}, &struct{}{})
}

// UpdateTrainingBalanceRequest 对应原余额请求的两个字段。
type UpdateTrainingBalanceRequest struct {
	UserID       string
	AmountChange int64
}
