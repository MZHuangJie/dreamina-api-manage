package dreamina

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"dreamina-manager/internal/browser"
)

// ImageModel 是文生图模型。
type ImageModel struct {
	ID         string `json:"id"`
	ReqKey     string `json:"reqKey"`
	Label      string `json:"label"`
	Resolution string `json:"resolution"`
	// BenefitType 用于商务信息；部分场景服务端会据此判定权益
	BenefitType string `json:"benefitType"`
}

// SeedreamV50 是默认模型（Seedream 5.0，2K）。
var SeedreamV50 = ImageModel{
	ID: "high_aes_general_v50", ReqKey: "high_aes_general_v50",
	Label: "Seedream 5.0", Resolution: "2k", BenefitType: "image_basic_v5_2k",
}

// GenerateImageInput 是一次文生图的入参。
type GenerateImageInput struct {
	Prompt      string
	Model       ImageModel
	WorkspaceID int64
	Width       int
	Height      int
	// ImageRatio 是官网的枚举值（16:9=1、1:1=8、9:16=6 …）
	ImageRatio int
	Seed       int64
}

func newID() string { return uuid.NewString() }

// Resolution 返回像素尺寸；缺省 2048×2048。
func (in GenerateImageInput) dimensions() (int, int) {
	if in.Width > 0 && in.Height > 0 {
		return in.Width, in.Height
	}
	return 2048, 2048
}

// buildImagePayload 构造 /mweb/v1/aigc_draft/generate 的请求体。
//
// 这里的字段集是照着真实成功抓包逐字段对齐的，几处反直觉但必须遵守：
//   - extend 里**只有** root_model + workspace_id，没有 commerce info
//   - metrics_extra 里**没有** sceneOptions，但要带 position / hasRejectedAudit
//   - draft_content.version 是 "3.3.28"（外层），min_version 才是 "3.0.2"
//   - component 上**没有** gen_type
//   - core_param 里**有** intelligent_ratio
//   - abilities.generate 里**没有** history_option
func buildImagePayload(in GenerateImageInput) (body map[string]any, metricsExtra string, genID string, err error) {
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, "", "", fmt.Errorf("提示词不能为空")
	}
	model := in.Model
	if model.ReqKey == "" {
		model = SeedreamV50
	}
	width, height := in.dimensions()
	ratio := in.ImageRatio
	if ratio == 0 {
		ratio = 1
	}
	seed := in.Seed
	if seed == 0 {
		seed = rand.Int63n(100_000_000) + 2_500_000_000
	}
	genID = newID()
	componentID := newID()
	resolution := model.Resolution
	if resolution == "" {
		resolution = "2k"
	}

	metrics := map[string]any{
		"promptSource":     "custom",
		"generateCount":    1,
		"enterFrom":        "click",
		"position":         "page_bottom_box",
		"isBoxSelect":      false,
		"isCutout":         false,
		"hasRejectedAudit": 0,
		"generateId":       genID,
		"isRegenerate":     false,
	}
	metricsBytes, err := json.Marshal(metrics)
	if err != nil {
		return nil, "", "", err
	}

	draft := map[string]any{
		"type":              "draft",
		"id":                newID(),
		"min_version":       "3.0.2",
		"min_features":      []any{},
		"is_from_tsn":       true,
		"version":           DaVersion,
		"main_component_id": componentID,
		"component_list": []any{
			map[string]any{
				"type":        "image_base_component",
				"id":          componentID,
				"min_version": "3.0.2",
				"aigc_mode":   "workbench",
				"metadata": map[string]any{
					"type":                     "",
					"id":                       newID(),
					"created_platform":         3,
					"created_platform_version": "",
					"created_time_in_ms":       strconv.FormatInt(time.Now().UnixMilli(), 10),
					"created_did":              "",
				},
				"generate_type": "generate",
				"abilities": map[string]any{
					"type": "",
					"id":   newID(),
					"generate": map[string]any{
						"type": "",
						"id":   newID(),
						"core_param": map[string]any{
							"type":            "",
							"id":              newID(),
							"model":           model.ReqKey,
							"prompt":          in.Prompt,
							"negative_prompt": "",
							"seed":            seed,
							"sample_strength": 0.5,
							"image_ratio":     ratio,
							"large_image_info": map[string]any{
								"type": "", "id": newID(),
								"height": height, "width": width, "resolution_type": resolution,
							},
							"intelligent_ratio": false,
							"generate_type":     0,
						},
					},
				},
			},
		},
	}
	draftBytes, err := json.Marshal(draft)
	if err != nil {
		return nil, "", "", err
	}

	body = map[string]any{
		"extend": map[string]any{
			"root_model":   model.ReqKey,
			"workspace_id": in.WorkspaceID,
		},
		"submit_id":        newID(),
		"metrics_extra":    string(metricsBytes),
		"draft_content":    string(draftBytes),
		"http_common_info": map[string]any{"aid": mustAtoi(AID)},
	}
	return body, string(metricsBytes), genID, nil
}

// buildBabiParam 构造 URL 上的埋点参数（需要二次 URL 编码）。
func buildBabiParam(model ImageModel) string {
	inner := map[string]any{"model_id": model.ReqKey, "generate_type": "1"}
	babi := map[string]any{
		"feature_entrance":        "to-generate",
		"feature_entrance_detail": "to-generate-" + model.ReqKey,
		"feature_key":             "aigc_to_image",
		"scenario":                "image_video_generation",
		"edit_type":               "tool",
		"tool_id":                 "tool_image",
		"sub_tool_id":             "tool_image",
		"tab_name":                "tool",
		"enter_from":              "tool",
		"template_id":             "",
		"scene_lv1":               "tool",
		"scene_lv2":               "tool_image",
		"extra_param":             inner,
	}
	raw, _ := json.Marshal(babi)
	// 抓包里是二次编码：{ -> %7B -> %257B
	return url.QueryEscape(url.QueryEscape(string(raw)))
}

// SubmitImageResult 是提交结果。
type SubmitImageResult struct {
	HistoryID string
	GenID     string
}

// SubmitImage 提交一次文生图。
func (c *Client) SubmitImage(ctx context.Context, account Account, in GenerateImageInput) (*SubmitImageResult, error) {
	body, _, genID, err := buildImagePayload(in)
	if err != nil {
		return nil, err
	}

	// 提交前先刷新签名
	pathname := "/mweb/v1/aigc_draft/generate"
	headers := account.headersFor(account.Endpoints, pathname)
	if err := c.sidecar.EnsureSession(ctx, account.ID, account.Cookies, browser.SessionOptions{
		Proxy:    account.Proxy,
		Timezone: TimezoneFor(account.Country),
	}); err != nil {
		return nil, err
	}

	extra := []string{
		"generate_id=gen-" + genID,
		"babi_param=" + buildBabiParam(in.Model),
		"commerce_with_input_video=1",
	}
	target := account.Endpoints.APIURL(pathname, extra...)

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	resp, err := c.sidecar.Fetch(ctx, browserFetch(account.ID, target, headers, string(encoded)))
	if err != nil {
		return nil, err
	}

	data, err := unwrap(resp.Body, pathname)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		AigcData struct {
			HistoryRecordID string `json:"history_record_id"`
		} `json:"aigc_data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析提交结果失败: %w", err)
	}
	if parsed.AigcData.HistoryRecordID == "" {
		return nil, fmt.Errorf("Dreamina 未返回 history_record_id，提交被拒绝")
	}
	return &SubmitImageResult{HistoryID: parsed.AigcData.HistoryRecordID, GenID: genID}, nil
}

// ImageResult 是轮询到的成品。
type ImageResult struct {
	HistoryID string
	Status    int
	URLs      []string
	FailCode  string
}

// PollImage 轮询直到出图或超时。
//
// 轮询是**读操作**，直连即可（实测不需要浏览器，也更快）。
func (c *Client) PollImage(ctx context.Context, account Account, historyID string, timeout time.Duration, onTick func(attempt, status int)) (*ImageResult, error) {
	pathname := "/mweb/v1/get_history_by_ids"
	deadline := time.Now().Add(timeout)
	attempt := 0

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt++

		raw, err := c.CallDirect(ctx, account, pathname, map[string]any{
			"history_ids":      []string{historyID},
			"http_common_info": map[string]any{"aid": mustAtoi(AID)},
		})
		if err != nil {
			return nil, err
		}

		// 响应是以 history_id 为键的 map，没有 ret 信封
		var byID map[string]historyRecord
		if err := json.Unmarshal(raw, &byID); err != nil {
			return nil, fmt.Errorf("解析轮询结果失败: %w", err)
		}
		rec, ok := byID[historyID]
		if !ok {
			return nil, fmt.Errorf("Dreamina 查不到生成记录 %s，可能已被删除", historyID)
		}
		if onTick != nil {
			onTick(attempt, rec.Status)
		}

		switch {
		case rec.Status == 30:
			return &ImageResult{HistoryID: historyID, Status: rec.Status, FailCode: rec.FailCode},
				fmt.Errorf("生成失败：%s", failReason(rec))
		case rec.Status != 20 && rec.Status != 42 && rec.Status != 45 && len(rec.ItemList) > 0:
			urls := extractImageURLs(rec.ItemList)
			if len(urls) > 0 {
				return &ImageResult{HistoryID: historyID, Status: rec.Status, URLs: urls}, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(PollInterval):
		}
	}
	return nil, fmt.Errorf("图片生成超时（已等待 %s）", timeout)
}

type historyRecord struct {
	Status   int               `json:"status"`
	FailCode string            `json:"fail_code"`
	ItemList []json.RawMessage `json:"item_list"`
}

func failReason(rec historyRecord) string {
	if rec.FailCode == "2038" {
		return "内容未通过平台审核（违规过滤）"
	}
	if rec.FailCode != "" {
		return fmt.Sprintf("状态码 %d，失败码 %s", rec.Status, rec.FailCode)
	}
	return fmt.Sprintf("状态码 %d", rec.Status)
}

func extractImageURLs(items []json.RawMessage) []string {
	var urls []string
	for _, item := range items {
		var parsed struct {
			Image struct {
				LargeImages []struct {
					ImageURL string `json:"image_url"`
				} `json:"large_images"`
			} `json:"image"`
			CommonAttr struct {
				CoverURL string `json:"cover_url"`
			} `json:"common_attr"`
		}
		if err := json.Unmarshal(item, &parsed); err != nil {
			continue
		}
		if len(parsed.Image.LargeImages) > 0 && parsed.Image.LargeImages[0].ImageURL != "" {
			urls = append(urls, parsed.Image.LargeImages[0].ImageURL)
		} else if parsed.CommonAttr.CoverURL != "" {
			urls = append(urls, parsed.CommonAttr.CoverURL)
		}
	}
	return urls
}

// ListWorkspaces 取该账号最新的 workspace id——提交生成时必填。
func (c *Client) ListWorkspaces(ctx context.Context, account Account) ([]int64, error) {
	data, err := c.CallDirect(ctx, account, "/mweb/v1/workspace/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Workspaces []struct {
			WorkspaceID int64 `json:"workspace_id"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析 workspace 列表失败: %w", err)
	}
	ids := make([]int64, 0, len(parsed.Workspaces))
	for _, w := range parsed.Workspaces {
		ids = append(ids, w.WorkspaceID)
	}
	return ids, nil
}

// Credit 是积分余额。
type Credit struct {
	GiftCredit     int64 `json:"gift_credit"`
	PurchaseCredit int64 `json:"purchase_credit"`
	VipCredit      int64 `json:"vip_credit"`
}

// Total 返回总积分。
func (c Credit) Total() int64 { return c.GiftCredit + c.PurchaseCredit + c.VipCredit }

// GetCredit 查询积分（读操作，直连）。
//
// 走 tryClusters：如果账号归属地判断错了，积分接口同样会返回 1015，
// 这里能顺带把正确的集群探测出来——而且它是**探活时第一个被调用的接口**，
// 所以等于在探活阶段就完成了集群发现。
func (c *Client) GetCredit(ctx context.Context, account Account) (*Credit, error) {
	pathname := "/commerce/v1/benefits/user_credit"

	proxy, _ := effectiveProxy(account.Proxy)

	raw, err := c.tryClusters(ctx, account, pathname, func(endpoints Endpoints) (json.RawMessage, error) {
		headers := account.headersFor(endpoints, pathname)
		headers["cookie"] = account.CookieHeader()
		resp, err := doJSON(ctx, endpoints.CommerceURL(pathname), headers, "{}", proxy)
		if err != nil {
			return nil, &fatalClusterError{err}
		}
		return json.RawMessage(resp), nil
	})
	if err != nil {
		return nil, err
	}

	var env struct {
		Ret      string `json:"ret"`
		ErrMsg   string `json:"errmsg"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("解析积分响应失败: %w", err)
	}
	if env.Ret != "0" {
		return nil, &APIError{Code: env.Ret, ErrMsg: env.ErrMsg, Path: pathname}
	}
	var inner struct {
		Credit Credit `json:"credit"`
	}
	if err := json.Unmarshal([]byte(env.Response), &inner); err != nil {
		return nil, fmt.Errorf("解析积分明细失败: %w", err)
	}
	return &inner.Credit, nil
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
