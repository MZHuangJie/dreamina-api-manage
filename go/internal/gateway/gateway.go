// Package gateway 是对外的聚合网关。
//
// 外部调用方用 baseURL + API Key 接入，就能使用管理器的账号池出图：
// 网关负责鉴权、参数翻译、挑账号、等结果，并且把请求记进生成历史。
//
// 协议刻意做成 **OpenAI 兼容**——这样现成的客户端、工作流工具、SDK
// 不用改代码就能接上，把管理器当成一个图片生成服务用。
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dreamina-manager/internal/store"
	"dreamina-manager/internal/tasks"
)

// Server 是网关的处理器。
type Server struct {
	DB    *store.DB
	Tasks *tasks.Manager
	// Models 返回可用的模型 ID（由上层从 Provider 取）。
	Models func() []string
	// WaitTimeout 是同步模式下等待出图的上限。
	WaitTimeout time.Duration
	// Limiter 做全局与按密钥的限流。非空时生效。
	Limiter *Limiter
	// Logf 可选，用于打印网关访问日志。
	Logf func(format string, args ...any)
}

// ModelAliases 把常见的外部模型名映射到本平台的模型。
//
// 有些客户端会硬编码 dall-e-3 / gpt-image-1，直接拒绝它们会让接入变得很麻烦，
// 所以统一落到默认模型上。
var ModelAliases = map[string]string{
	"dall-e-3":    "high_aes_general_v50",
	"dall-e-2":    "high_aes_general_v40",
	"gpt-image-1": "high_aes_general_v50p_large",
	"seedream":    "high_aes_general_v50",
	"default":     "high_aes_general_v50",
}

// DefaultModel 是未指定模型时使用的模型。
const DefaultModel = "high_aes_general_v50"

// Handler 返回网关路由（挂在 /v1 下）。
//
// 这些路径**不经过**管理端的访问令牌校验——它们用自己的 Bearer 密钥。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/images/generations", s.authenticated(s.imageGenerations))
	mux.HandleFunc("GET /v1/images/generations/{id}", s.authenticated(s.imageStatus))
	mux.HandleFunc("GET /v1/models", s.authenticated(s.listModels))
	mux.HandleFunc("GET /v1/models/{id}", s.authenticated(s.getModel))
	return mux
}

type ctxKey string

const apiKeyCtxKey ctxKey = "gateway.apiKey"

// authenticated 校验 Bearer 密钥并把密钥放进上下文。
func (s *Server) authenticated(next func(http.ResponseWriter, *http.Request, *store.APIKey)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			// 兼容部分客户端把密钥放在 x-api-key 里
			token = r.Header.Get("x-api-key")
		}
		key, err := s.DB.VerifyAPIKey(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_api_key",
				"缺少或无效的 API Key，请在 Authorization 头里带 Bearer <key>", "authentication_error")
			return
		}
		next(w, r, key)
	}
}

func bearerToken(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("authorization"))
	if raw == "" {
		return ""
	}
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return raw
}

/* ------------------------------ 请求 / 响应 ------------------------------ */

// imageRequest 是 OpenAI 图片生成请求的子集。
type imageRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	Style          string `json:"style"`
	ResponseFormat string `json:"response_format"`
	User           string `json:"user"`
	// 以下是本网关的扩展字段
	AccountID string `json:"account_id"`
	Strategy  string `json:"strategy"`
	Ratio     string `json:"ratio"`
}

type imageResponse struct {
	Created int64        `json:"created"`
	Data    []imageDatum `json:"data"`
	// 扩展字段：方便调用方回查管理器里的记录
	TaskID string `json:"task_id,omitempty"`
	Model  string `json:"model,omitempty"`
}

type imageDatum struct {
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// imageGenerations 是核心接口：提交生成并（默认）等到出图再返回。
func (s *Server) imageGenerations(w http.ResponseWriter, r *http.Request, key *store.APIKey) {
	var req imageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"请求体不是合法 JSON: "+err.Error(), "invalid_request_error")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"prompt 不能为空", "invalid_request_error")
		return
	}

	// —— 限流 ——
	//
	// 顺序很重要：先取并发与频次许可（内存计数，快），再扣每日配额（落库）。
	// 反过来会出现「配额扣了但请求被频次限制拒绝」的浪费。
	if s.Limiter != nil {
		release, err := s.Limiter.Acquire(key.ID, Limits{
			PerMinute:     key.RatePerMinute,
			MaxConcurrent: key.MaxConcurrent,
			DailyQuota:    key.DailyQuota,
		})
		if err != nil {
			status, code := http.StatusTooManyRequests, "rate_limit_exceeded"
			if errors.Is(err, ErrServerBusy) {
				status, code = http.StatusServiceUnavailable, "server_busy"
			}
			w.Header().Set("retry-after", "5")
			writeError(w, status, code, err.Error(), "rate_limit_error")
			return
		}
		defer release()
	}

	if key.DailyQuota > 0 {
		used, ok, err := s.DB.ConsumeQuota(key.ID, key.DailyQuota)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "server_error", err.Error(), "server_error")
			return
		}
		if !ok {
			writeError(w, http.StatusTooManyRequests, "quota_exceeded",
				fmt.Sprintf("已用完今日配额（%d/%d），明日重置", used, key.DailyQuota), "rate_limit_error")
			return
		}
	}

	width, height, ratioCode := resolveSize(req.Size, req.Ratio)
	model := resolveModel(req.Model)
	strategy := store.ParseStrategy(req.Strategy)

	record, err := s.Tasks.Create(r.Context(), tasks.CreateInput{
		Mode:       "text2image",
		Prompt:     req.Prompt,
		AccountID:  req.AccountID,
		Strategy:   strategy,
		Provider:   "dreamina",
		ModelID:    model,
		ImageRatio: ratioCode,
		Width:      width,
		Height:     height,
		APIKeyID:   key.ID,
	})
	if err != nil {
		status, code := statusForError(err), "generation_failed"
		if errors.Is(err, tasks.ErrTooBusy) {
			status, code = http.StatusServiceUnavailable, "server_busy"
			w.Header().Set("retry-after", "10")
		}
		writeError(w, status, code, err.Error(), "server_error")
		return
	}

	s.DB.TouchAPIKey(key.ID)
	s.DB.LogEvent(store.Event{
		AccountID: record.AccountID,
		Kind:      "gateway.request",
		Message:   "外部调用（密钥「" + key.Name + "」）：" + truncate(req.Prompt, 60),
	})

	// 异步模式：直接把任务 id 还给调用方，让它自己轮询
	if r.URL.Query().Get("async") == "1" || r.URL.Query().Get("async") == "true" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"id":     record.ID,
			"status": record.Status,
			"object": "image.generation.task",
		})
		return
	}

	final, err := s.wait(r.Context(), record.ID)
	if err != nil {
		writeError(w, statusForError(err), "generation_failed", err.Error(), "server_error")
		return
	}
	if final.Status != tasks.StatusSucceeded || final.Result == nil || len(final.Result.URLs) == 0 {
		message := "生成未成功"
		if final.Error != nil {
			message = *final.Error
		}
		writeError(w, http.StatusBadGateway, "generation_failed", message, "server_error")
		return
	}

	data := make([]imageDatum, 0, len(final.Result.URLs))
	for _, u := range final.Result.URLs {
		data = append(data, imageDatum{URL: u, RevisedPrompt: req.Prompt})
	}
	writeJSON(w, http.StatusOK, imageResponse{
		Created: time.Now().Unix(),
		Data:    data,
		TaskID:  final.ID,
		Model:   model,
	})
}

// imageStatus 用于异步模式轮询。
func (s *Server) imageStatus(w http.ResponseWriter, r *http.Request, _ *store.APIKey) {
	record, err := s.Tasks.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "找不到该生成任务", "invalid_request_error")
		return
	}
	payload := map[string]any{
		"id":     record.ID,
		"status": record.Status,
		"object": "image.generation.task",
	}
	if record.Progress != nil {
		payload["progress"] = *record.Progress
	}
	if record.Error != nil {
		payload["error"] = *record.Error
	}
	if record.Result != nil && len(record.Result.URLs) > 0 {
		data := make([]imageDatum, 0, len(record.Result.URLs))
		for _, u := range record.Result.URLs {
			data = append(data, imageDatum{URL: u})
		}
		payload["data"] = data
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request, _ *store.APIKey) {
	ids := []string{}
	if s.Models != nil {
		ids = s.Models()
	}
	data := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"owned_by": "dreamina-manager",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request, _ *store.APIKey) {
	id := resolveModel(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "object": "model", "owned_by": "dreamina-manager",
	})
}

/* ------------------------------ 内部工具 ------------------------------ */

// wait 轮询任务直到进入终态或超时。
func (s *Server) wait(ctx context.Context, id string) (*tasks.Record, error) {
	timeout := s.WaitTimeout
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := s.Tasks.Get(id)
		if err != nil {
			return nil, err
		}
		switch record.Status {
		case tasks.StatusSucceeded, tasks.StatusFailed, tasks.StatusCanceled:
			return record, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待出图超时（%s），任务 %s 仍在后台运行，可用 /v1/images/generations/%s 查询", timeout, id, id)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// resolveModel 把外部模型名翻译成本平台模型。
func resolveModel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultModel
	}
	if mapped, ok := ModelAliases[strings.ToLower(name)]; ok {
		return mapped
	}
	return name
}

// ratioCodes 把宽高比映射到平台的 image_ratio 枚举。
//
// 顺序即枚举值：21:9=0, 16:9=1, 3:2=2, 4:3=3, 3:4=4, 2:3=5, 9:16=6, 1:1=8。
var ratioCodes = []struct {
	Code   int
	Aspect float64
}{
	{0, 21.0 / 9.0},
	{1, 16.0 / 9.0},
	{2, 3.0 / 2.0},
	{3, 4.0 / 3.0},
	{4, 3.0 / 4.0},
	{5, 2.0 / 3.0},
	{6, 9.0 / 16.0},
	{8, 1.0},
}

// resolveSize 解析 size / ratio 参数。
//
// 默认按 1:1 出 2048×2048——这是平台的原生分辨率，
// 强行要 1024 只会被放大，没有意义，所以尺寸只用来决定**宽高比**。
func resolveSize(size, ratio string) (width, height, ratioCode int) {
	const native = 2048

	if ratio != "" {
		if code, ok := parseRatioString(ratio); ok {
			w, h := dimensionsForRatio(code, native)
			return w, h, code
		}
	}

	size = strings.ToLower(strings.TrimSpace(size))
	if size == "" || size == "auto" {
		return native, native, 8
	}

	parts := strings.SplitN(size, "x", 2)
	if len(parts) != 2 {
		return native, native, 8
	}
	sw, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	sh, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || sw <= 0 || sh <= 0 {
		return native, native, 8
	}

	aspect := float64(sw) / float64(sh)
	best, bestDiff := 8, math.MaxFloat64
	for _, candidate := range ratioCodes {
		diff := math.Abs(candidate.Aspect - aspect)
		if diff < bestDiff {
			best, bestDiff = candidate.Code, diff
		}
	}
	w, h := dimensionsForRatio(best, native)
	return w, h, best
}

func parseRatioString(ratio string) (int, bool) {
	switch strings.TrimSpace(ratio) {
	case "21:9":
		return 0, true
	case "16:9":
		return 1, true
	case "3:2":
		return 2, true
	case "4:3":
		return 3, true
	case "3:4":
		return 4, true
	case "2:3":
		return 5, true
	case "9:16":
		return 6, true
	case "1:1":
		return 8, true
	}
	return 0, false
}

// dimensionsForRatio 按宽高比算出一个长边为 base 的尺寸（取 64 的整数倍）。
func dimensionsForRatio(code, base int) (int, int) {
	aspect := 1.0
	for _, candidate := range ratioCodes {
		if candidate.Code == code {
			aspect = candidate.Aspect
			break
		}
	}
	if aspect >= 1 {
		w := base
		h := roundTo64(float64(base) / aspect)
		return w, h
	}
	h := base
	w := roundTo64(float64(base) * aspect)
	return w, h
}

func roundTo64(v float64) int {
	n := int(math.Round(v/64)) * 64
	if n < 64 {
		n = 64
	}
	return n
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrKeyInvalid):
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError 用 OpenAI 的错误信封返回，客户端能直接解析。
func writeError(w http.ResponseWriter, status int, code, message, errType string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"code":    code,
			"param":   nil,
		},
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
