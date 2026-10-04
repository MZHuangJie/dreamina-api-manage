package dreamina

import (
	"context"
	"encoding/json"
	"fmt"
)

// creditReceivePath 是每日赠送的领取接口。
//
// 这个路径是从 Dreamina 自己的前端 JS 包里挖出来的——
// 靠猜是猜不到的（我猜了 7 个路径全部 403）。
// 同一份 JS 里还有 /commerce/v3/benefits/batch_get_user_benefit 等 v3 接口。
const creditReceivePath = "/commerce/v1/benefits/credit_receive"

// ClaimDailyCredit 尝试领取每日赠送积分。
//
// 返回平台给的原始响应，便于看清楚「领到了多少」还是「为什么不能领」。
// 已经领过时平台通常会返回一个非 0 的 ret，这时会包成 APIError 返回。
func (c *Client) ClaimDailyCredit(ctx context.Context, account Account) (json.RawMessage, error) {
	// time_zone 影响「今天」怎么算——平台按这个时区判定当日是否已领取。
	// 用账号所在地的时区更符合它的预期。
	body := map[string]any{"time_zone": timezoneForClaim(account.Country)}

	data, err := c.CallCommerceBrowser(ctx, account, creditReceivePath, body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// timezoneForClaim 把国家映射成领取时用的时区。
func timezoneForClaim(country string) string {
	if tz := TimezoneFor(country); tz != "" {
		return tz
	}
	return "Asia/Shanghai"
}

// CreditReceiveInfo 是领取接口的解析结果（能解析出来时）。
type CreditReceiveInfo struct {
	// ReceiveQuota 是本次领到的数量。
	ReceiveQuota int64 `json:"receive_quota"`
	// CurTotalCredit 是领取后的总额。
	CurTotalCredit int64 `json:"cur_total_credits"`
}

// ParseCreditReceive 尝试解析领取响应；字段名不确定时返回零值。
func ParseCreditReceive(data json.RawMessage) (CreditReceiveInfo, error) {
	var info CreditReceiveInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("解析领取响应失败: %w", err)
	}
	return info, nil
}
