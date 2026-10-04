package api

import (
	"net/http"

	"dreamina-manager/internal/provider"
	"dreamina-manager/internal/store"
	"dreamina-manager/internal/tasks"
)

// models 返回模型目录，供前端渲染下拉框。
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platform = "dreamina"
	}
	imageModels := []provider.ImageModel{}
	if adapter, err := s.Providers.For(platform); err == nil {
		if list, err := adapter.Models(r.Context(), provider.Account{}); err == nil {
			imageModels = list
		}
	}

	type modelView struct {
		ID                string   `json:"id"`
		ReqKey            string   `json:"reqKey"`
		Label             string   `json:"label"`
		Resolutions       []string `json:"resolutions"`
		DefaultResolution string   `json:"defaultResolution"`
		CreditPerImage    int64    `json:"creditPerImage"`
	}
	views := make([]modelView, 0, len(imageModels))
	for _, m := range imageModels {
		views = append(views, modelView{
			ID: m.ID, ReqKey: m.ID, Label: m.Label,
			Resolutions:       []string{"1k", "2k", "4k"},
			DefaultResolution: m.Resolution,
			CreditPerImage:    m.CreditEstimate,
		})
	}

	ok(w, map[string]any{
		"image":       views,
		"video":       []any{},
		"imageRatios": []string{"21:9", "16:9", "3:2", "4:3", "1:1", "3:4", "2:3", "9:16"},
		"videoRatios": []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
		"platforms":   s.Providers.Names(),
	})
}

func (s *Server) createGeneration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind       string `json:"kind"`
		Mode       string `json:"mode"`
		Prompt     string `json:"prompt"`
		AccountID  string `json:"accountId"`
		Strategy   string `json:"strategy"`
		Provider   string `json:"provider"`
		ModelID    string `json:"modelId"`
		ImageRatio int    `json:"imageRatio"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}
	if req.Kind == "video" {
		fail(w, http.StatusNotImplemented, "NOT_SUPPORTED", "视频生成尚未接入")
		return
	}
	strategy := store.ParseStrategy(req.Strategy)
	if req.Strategy == "" {
		if fromDB, err := s.DB.GetSetting("selection.strategy"); err == nil && fromDB != "" {
			strategy = store.ParseStrategy(fromDB)
		}
	}

	record, err := s.Tasks.Create(r.Context(), tasks.CreateInput{
		Mode:       defaultString(req.Mode, "text2image"),
		Prompt:     req.Prompt,
		AccountID:  req.AccountID,
		Strategy:   strategy,
		Provider:   req.Provider,
		ModelID:    req.ModelID,
		ImageRatio: req.ImageRatio,
		Width:      req.Width,
		Height:     req.Height,
	})
	if err != nil {
		failErr(w, err)
		return
	}
	okStatus(w, http.StatusAccepted, record)
}

func (s *Server) listGenerations(w http.ResponseWriter, r *http.Request) {
	records, err := s.Tasks.List(queryInt(r, "limit", 50))
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, records)
}

func (s *Server) getGeneration(w http.ResponseWriter, r *http.Request) {
	record, err := s.Tasks.Get(r.PathValue("id"))
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, record)
}

func (s *Server) cancelGeneration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Tasks.Cancel(id); err != nil {
		failErr(w, err)
		return
	}
	record, err := s.Tasks.Get(id)
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, record)
}

func (s *Server) deleteGeneration(w http.ResponseWriter, r *http.Request) {
	if err := s.Tasks.Delete(r.PathValue("id")); err != nil {
		failErr(w, err)
		return
	}
	ok(w, map[string]any{"deleted": true})
}

func (s *Server) generationStats(w http.ResponseWriter, r *http.Request) {
	var total int
	var runningN, succeededN, failedN, imagesN, videosN *int
	row := s.DB.SQL().QueryRow(`
		SELECT COUNT(*),
			SUM(CASE WHEN status IN ('pending','running') THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN mode LIKE '%image%' THEN 1 ELSE 0 END),
			SUM(CASE WHEN mode LIKE '%video%' THEN 1 ELSE 0 END)
		FROM generations`)
	if err := row.Scan(&total, &runningN, &succeededN, &failedN, &imagesN, &videosN); err != nil {
		failErr(w, err)
		return
	}
	deref := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	ok(w, map[string]any{
		"total":     total,
		"running":   deref(runningN),
		"succeeded": deref(succeededN),
		"failed":    deref(failedN),
		"images":    deref(imagesN),
		"videos":    deref(videosN),
	})
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
