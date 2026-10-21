package budget

import (
	"context"
	"errors"

	"github.com/secacy/agent-harness/internal/agent"
)

// ProfiledModel 必须由宿主组装，返回与真实请求配置相同的上界材料。
type ProfiledModel interface {
	agent.Model
	BudgetProfile() (Profile, error)
}

type Model struct {
	ledger     *Ledger
	underlying ProfiledModel
	target     string
	run        string
	reference  Reference
}

// NewModel 认证模型配置并绑定 Run；未提供已核实配置的模型不能加入账本。
func NewModel(ledger *Ledger, target, run string, model agent.Model) (*Model, error) {
	if ledger == nil || model == nil {
		return nil, errors.New("ledger and model are required")
	}
	provider, ok := model.(ProfiledModel)
	if !ok {
		return nil, errors.New("model does not provide a verified budget profile")
	}
	profile, err := provider.BudgetProfile()
	if err != nil {
		return nil, err
	}
	if err = profile.Validate(); err != nil {
		return nil, err
	}
	profile.VerifiedAt = profile.VerifiedAt.UTC()
	state := ledger.Snapshot()
	// 用规范化摘要比较，时间时区表示不影响配置身份。
	profileSHA, _ := digestJSON(profile)
	expectedSHA, _ := digestJSON(state.Profile)
	if profileSHA != expectedSHA {
		return nil, errors.New("model budget profile does not match the campaign")
	}
	if err = ledger.bind(target, run); err != nil {
		return nil, err
	}
	return &Model{ledger: ledger, underlying: provider, target: target, run: run, reference: Reference{state.CampaignID, target, run, ledger.path, profileSHA}}, nil
}

func (m *Model) Reference() Reference { return m.reference }

func (m *Model) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (reply agent.Reply, err error) {
	if !m.ledger.deadline.IsZero() {
		var stop context.CancelFunc
		ctx, stop = context.WithDeadline(ctx, m.ledger.deadline)
		defer stop()
	}
	if err = ctx.Err(); err != nil {
		return reply, err
	}
	input, err := digestJSON(struct {
		Messages []agent.Message
		Tools    []agent.ToolSpec
	}{messages, tools})
	if err != nil {
		return reply, err
	}
	call, err := m.ledger.reserve(ctx, m.target, m.run, input)
	if err != nil {
		return reply, err
	}
	// 共享账本故障取消在途调用；结算仍在调用返回后独立执行。
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ledger.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err = callCtx.Err(); err == nil {
		reply, err = m.underlying.Complete(callCtx, messages, tools)
	}
	outcome := "reply"
	if err != nil {
		outcome = "model_error"
	}
	settleErr := m.ledger.settle(call.ID, reply.Usage, outcome)
	if fault := m.ledger.Err(); fault != nil {
		settleErr = errors.Join(settleErr, ErrHalted, fault)
	}
	return reply, errors.Join(err, settleErr)
}
