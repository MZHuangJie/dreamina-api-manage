package provider

import (
	"context"
	"errors"
	"time"

	"dreamina-manager/internal/browser"
	"dreamina-manager/internal/crypto"
	"dreamina-manager/internal/dreamina"
)

// Dreamina 是海外版（dreamina.capcut.com）的适配器。
type Dreamina struct {
	client *dreamina.Client
}

// NewDreamina 创建 Dreamina 适配器。
func NewDreamina(client *dreamina.Client) *Dreamina {
	return &Dreamina{client: client}
}

// Name 实现 Provider。
func (d *Dreamina) Name() string { return "dreamina" }

// cookiejarDomain 是 Dreamina 会话 cookie 的归属域。
const cookiejarDomain = ".capcut.com"

// toAccount 把统一账号上下文翻译成 Dreamina 客户端需要的形态。
//
// Cookie 的构造是平台特定知识：Dreamina 用 .capcut.com 域下的 sessionid 系列，
// 外加决定集群归属的 store-idc。
func toAccount(acct Account) dreamina.Account {
	// 归属地优先从**凭据**里现取，库里存的可能是添加时抽的、已经过时了
	storeIDC := acct.StoreIDC
	country := acct.StoreCountry
	if idc := crypto.ExtractCookieValue(acct.Credential, "store-idc"); idc != "" {
		storeIDC = idc
	}
	if cc := crypto.ExtractCookieValue(acct.Credential, "store-country-code"); cc != "" {
		country = cc
	}

	// 首选集群按归属地推断；推断不出来也没关系——
	// 客户端遇到 1015 会依次试候选并记住成功的那一个。
	endpoints, err := dreamina.ResolveEndpoints(storeIDC, country)
	if err != nil {
		endpoints = dreamina.Candidates(storeIDC, country)[0]
	}

	sessionID, _ := crypto.ExtractSessionID(acct.Credential)
	cookies := []browser.Cookie{
		{Name: "sessionid", Value: sessionID, Domain: cookiejarDomain, Path: "/"},
		{Name: "sessionid_ss", Value: sessionID, Domain: cookiejarDomain, Path: "/"},
		{Name: "sid_tt", Value: sessionID, Domain: cookiejarDomain, Path: "/"},
		{Name: "store-idc", Value: storeIDC, Domain: cookiejarDomain, Path: "/"},
		{Name: "store-country-code", Value: country, Domain: cookiejarDomain, Path: "/"},
	}
	// 时区提示：浏览器通道据此设置页面时区。
	// 账号在日区、代理在日本，浏览器却报美国时间是很显眼的指纹差异。
	if tz := dreamina.TimezoneFor(country); tz != "" {
		cookies = append(cookies, browser.Cookie{Name: "x-manager-timezone", Value: tz, Domain: cookiejarDomain, Path: "/"})
	}
	if csrf := crypto.ExtractCookieValue(acct.Credential, "passport_csrf_token"); csrf != "" {
		cookies = append(cookies,
			browser.Cookie{Name: "passport_csrf_token", Value: csrf, Domain: cookiejarDomain, Path: "/"},
			browser.Cookie{Name: "passport_csrf_token_default", Value: csrf, Domain: cookiejarDomain, Path: "/"},
		)
	}

	return dreamina.Account{
		ID:        acct.ID,
		Cookies:   cookies,
		Proxy:     acct.Proxy,
		Endpoints: endpoints,
		Country:   country,
		StoreIDC:  storeIDC,
	}
}

// Probe 实现 Provider。
func (d *Dreamina) Probe(ctx context.Context, acct Account) (*ProbeResult, error) {
	credit, err := d.client.GetCredit(ctx, toAccount(acct))
	if err != nil {
		return nil, translate(err)
	}
	return &ProbeResult{
		Credit: Credit{
			Free:     ptr(credit.GiftCredit),
			Purchase: ptr(credit.PurchaseCredit),
			Vip:      ptr(credit.VipCredit),
			Total:    ptr(credit.Total()),
		},
	}, nil
}

// GetCredit 实现 Provider。
func (d *Dreamina) GetCredit(ctx context.Context, acct Account) (*Credit, error) {
	credit, err := d.client.GetCredit(ctx, toAccount(acct))
	if err != nil {
		return nil, translate(err)
	}
	return &Credit{
		Free:     ptr(credit.GiftCredit),
		Purchase: ptr(credit.PurchaseCredit),
		Vip:      ptr(credit.VipCredit),
		Total:    ptr(credit.Total()),
	}, nil
}

// ListWorkspaces 实现 Provider。提交生成时 workspace 必填。
func (d *Dreamina) ListWorkspaces(ctx context.Context, acct Account) ([]int64, error) {
	ids, err := d.client.ListWorkspaces(ctx, toAccount(acct))
	return ids, translate(err)
}

// Models 实现 Provider。
func (d *Dreamina) Models(_ context.Context, _ Account) ([]ImageModel, error) {
	return []ImageModel{
		{ID: "high_aes_general_v50p_large", Label: "Seedream 5.0 Pro", Resolution: "2k", CreditEstimate: 8},
		{ID: "high_aes_general_v50", Label: "Seedream 5.0", Resolution: "2k", CreditEstimate: 4},
		{ID: "high_aes_general_v42", Label: "Seedream 4.6", Resolution: "2k", CreditEstimate: 4},
		{ID: "high_aes_general_v41", Label: "Seedream 4.1", Resolution: "2k", CreditEstimate: 4},
		{ID: "high_aes_general_v40", Label: "Seedream 4.0", Resolution: "2k", CreditEstimate: 4},
	}, nil
}

// SubmitImage 实现 Provider。
func (d *Dreamina) SubmitImage(ctx context.Context, acct Account, req ImageRequest) (*SubmitResult, error) {
	model := dreamina.SeedreamV50
	if req.ModelID != "" {
		model.ReqKey = req.ModelID
		model.ID = req.ModelID
	}
	result, err := d.client.SubmitImage(ctx, toAccount(acct), dreamina.GenerateImageInput{
		Prompt:      req.Prompt,
		Model:       model,
		WorkspaceID: req.WorkspaceID,
		ImageRatio:  req.ImageRatio,
		Width:       req.Width,
		Height:      req.Height,
	})
	if err != nil {
		return nil, translate(err)
	}
	return &SubmitResult{HistoryID: result.HistoryID}, nil
}

// PollImage 实现 Provider。
func (d *Dreamina) PollImage(ctx context.Context, acct Account, historyID string, timeout time.Duration, onTick TickFunc) (*ImageResult, error) {
	result, err := d.client.PollImage(ctx, toAccount(acct), historyID, timeout, func(attempt, status int) {
		if onTick != nil {
			onTick(attempt, status)
		}
	})
	if err != nil {
		return nil, translate(err)
	}
	return &ImageResult{Status: result.Status, URLs: result.URLs}, nil
}

// translate 把平台错误翻译成上层认识的两类关键错误。
func translate(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *dreamina.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "1015", "1000", "1001", "8":
			return &AuthError{Message: "登录态已失效：" + apiErr.ErrMsg}
		case "5000", "1006", "34070104":
			return &InsufficientCreditError{Message: "积分或权益不足：" + apiErr.ErrMsg}
		}
	}
	if errors.Is(err, dreamina.ErrPermissionDenied) {
		return &AuthError{Message: "平台拒绝了生成请求（权限不足），请确认账号可用且地区正确"}
	}
	return err
}

func ptr(v int64) *int64 { return &v }
