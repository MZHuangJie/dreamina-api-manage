package dreamina

import (
	"context"
	"encoding/json"
)

// 积分流水的动作类型。
//
// 注意：**amount 字段永远是正数**，方向由 history_type 决定。
// 早期实现假设「负数表示消耗」，结果把账算反了——
// 消耗被当成获得，余额怎么都对不上。
const (
	// HistoryTypeReward 表示获得（任务奖励、活动赠送等）。
	HistoryTypeReward = 1
	// HistoryTypeConsume 表示消耗（生成图片/视频等）。
	HistoryTypeConsume = 2
)

// CreditRecord 是一条积分流水。
type CreditRecord struct {
	// Amount 是变动数量，**恒为正**，方向看 HistoryType。
	Amount int64 `json:"amount"`
	// Title 是平台给的可读描述，如 "Generate image" / "Task Reward"。
	Title string `json:"title"`
	// HistoryType 决定这是获得还是消耗。
	HistoryType int `json:"history_type"`
	// CreateTime 是 Unix 秒。
	CreateTime int64 `json:"create_time"`
	// Status 如 "Checked"。
	Status string `json:"status"`
	// HistoryID 关联到生成记录。
	HistoryID string `json:"history_id"`
	// SubmitID 是当次生成的提交 id。
	SubmitID string `json:"submit_id"`
}

// Delta 返回带符号的变动量：获得为正，消耗为负。
func (r CreditRecord) Delta() int64 {
	if r.HistoryType == HistoryTypeConsume {
		return -r.Amount
	}
	return r.Amount
}

// IsReward 判断这条是不是获得。
func (r CreditRecord) IsReward() bool { return r.HistoryType != HistoryTypeConsume }

// CreditGrant 是一笔有有效期的赠送额度。
type CreditGrant struct {
	ResidualCredits int64 `json:"residual_credits"`
	// LifeEnd 是到期时间（Unix 秒）。
	LifeEnd int64 `json:"credits_life_end"`
}

// CreditOverview 是积分总览。
type CreditOverview struct {
	Credit Credit `json:"credit"`
	// Grants 是各笔赠送的剩余与到期时间。
	Grants []CreditGrant `json:"grants"`
}

// GetCreditOverview 查询积分总览（含每笔赠送的有效期）。
func (c *Client) GetCreditOverview(ctx context.Context, account Account) (*CreditOverview, error) {
	data, err := c.CallCommerce(ctx, account, "/commerce/v1/benefits/user_credit", map[string]any{})
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Credit        Credit `json:"credit"`
		CreditsDetail struct {
			GiftCredits []CreditGrant `json:"gift_credits"`
			Credits     []CreditGrant `json:"credits"`
		} `json:"credits_detail"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}

	grants := parsed.CreditsDetail.GiftCredits
	if len(grants) == 0 {
		grants = parsed.CreditsDetail.Credits
	}
	return &CreditOverview{Credit: parsed.Credit, Grants: grants}, nil
}

// CreditHistory 查询积分流水。
//
// 这是回答「我的积分怎么没变 / 怎么少了」最直接的接口——
// 它逐条列出每次获得和消耗。
func (c *Client) CreditHistory(ctx context.Context, account Account, count int) ([]CreditRecord, error) {
	if count <= 0 || count > 200 {
		count = 50
	}
	// 必须用 Envelope 变体：这个接口把数据放在顶层的 response 字段，
	// 而不是像 user_credit 那样放在 data 里。平台自己不一致。
	data, err := c.CallCommerceEnvelope(ctx, account, "/commerce/v1/benefits/user_credit_history", map[string]any{
		"count": count,
	})
	if err != nil {
		return nil, err
	}

	// 而且 response 里装的是字符串形式的 JSON，双层嵌套。
	var envelope struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if envelope.Response == "" {
		return nil, nil
	}

	var parsed struct {
		Records []CreditRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(envelope.Response), &parsed); err != nil {
		return nil, err
	}
	return parsed.Records, nil
}
