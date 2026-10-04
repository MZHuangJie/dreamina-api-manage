package provider

import (
	"errors"
	"fmt"
	"testing"
)

func TestAccountFault(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// —— 账号自身问题：换号有意义 ——
		{"登录态失效", &AuthError{Message: "登录态已失效：1015"}, true},
		{"权限不足", &AuthError{Message: "平台拒绝了生成请求（权限不足）"}, true},
		{"积分不足", &InsufficientCreditError{Message: "积分或权益不足：5000"}, true},
		{"没有 workspace", ErrNoWorkspace, true},
		{"包装过的登录失效", fmt.Errorf("取 workspace 失败: %w", &AuthError{Message: "1015"}), true},

		// —— 网络/代理问题：换出口有意义 ——
		{"代理连不上", errors.New("经代理 http://127.0.0.1:7897 请求 Dreamina 失败: connection refused"), true},
		{"请求超时", errors.New("context deadline exceeded"), true},
		{"名字解析失败", errors.New("dial tcp: lookup dreamina-api.us.capcut.com: no such host"), true},
		{"风控拦截", errors.New("shark not pass reject"), true},

		// —— 换号没用：不该重试 ——
		{"内容审核", ErrContentRejected, false},
		{"审核（中文文案）", errors.New("生成失败：内容未通过平台审核（违规过滤）"), false},
		{"审核（英文文案）", errors.New("blocked by content policy"), false},
		{"空提示词", errors.New("提示词不能为空"), false},
		{"参数非法", errors.New("image_ratio 超出范围"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AccountFault(tc.err); got != tc.want {
				t.Errorf("AccountFault(%v) = %v，期望 %v", tc.err, got, tc.want)
			}
		})
	}
}

// 审核类错误必须优先于「账号问题」判定——否则同一个 prompt 会被
// 拿去把所有账号都撞一遍，纯粹浪费额度。
func TestContentRejectedWinsOverAuthError(t *testing.T) {
	// 既是 AuthError 又命中审核文案时，应当判为不可重试
	err := fmt.Errorf("%w: %w", &AuthError{Message: "内容未通过平台审核"}, ErrContentRejected)
	if AccountFault(err) {
		t.Fatal("同时命中审核语义时不应判定为可重试")
	}
}
