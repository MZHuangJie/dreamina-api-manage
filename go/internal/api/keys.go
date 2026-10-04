package api

import (
	"net/http"

	"dreamina-manager/internal/store"
)

// listKeys 返回全部网关密钥（不含明文）。
func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.DB.ListAPIKeys()
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, keys)
}

// createKey 创建一把密钥。**明文只在这次响应里出现**，之后再也取不回。
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		Remark        string `json:"remark"`
		RatePerMinute int    `json:"ratePerMinute"`
		MaxConcurrent int    `json:"maxConcurrent"`
		DailyQuota    int64  `json:"dailyQuota"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}
	if req.RatePerMinute < 0 || req.MaxConcurrent < 0 || req.DailyQuota < 0 {
		fail(w, http.StatusBadRequest, "VALIDATION", "配额不能为负数")
		return
	}
	key, plain, err := s.DB.CreateAPIKey(store.CreateAPIKeyInput{
		Name:          req.Name,
		Remark:        req.Remark,
		RatePerMinute: req.RatePerMinute,
		MaxConcurrent: req.MaxConcurrent,
		DailyQuota:    req.DailyQuota,
	})
	if err != nil {
		failErr(w, err)
		return
	}
	s.DB.LogEvent(store.Event{
		Kind:    "gateway.key_create",
		Message: "创建网关密钥「" + key.Name + "」",
	})
	okStatus(w, http.StatusCreated, map[string]any{
		"key": key,
		// 明文仅此一次
		"secret": plain,
	})
}

// updateKey 启用/吊销密钥。
func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		fail(w, http.StatusBadRequest, "VALIDATION", "需要 enabled 字段")
		return
	}
	id := r.PathValue("id")
	if err := s.DB.SetAPIKeyEnabled(id, *req.Enabled); err != nil {
		failErr(w, err)
		return
	}
	key, err := s.DB.GetAPIKey(id)
	if err != nil {
		failErr(w, err)
		return
	}
	action := "吊销"
	if *req.Enabled {
		action = "启用"
	}
	s.DB.LogEvent(store.Event{
		Level:   levelFor(*req.Enabled),
		Kind:    "gateway.key_toggle",
		Message: action + "网关密钥「" + key.Name + "」",
	})
	ok(w, key)
}

// deleteKey 删除密钥。
func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	key, err := s.DB.GetAPIKey(id)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := s.DB.DeleteAPIKey(id); err != nil {
		failErr(w, err)
		return
	}
	s.DB.LogEvent(store.Event{
		Level:   "warn",
		Kind:    "gateway.key_delete",
		Message: "删除网关密钥「" + key.Name + "」",
	})
	ok(w, map[string]any{"deleted": true})
}

func levelFor(enabled bool) string {
	if enabled {
		return "info"
	}
	return "warn"
}
