package api

import (
	"net/http"
	"strconv"
	"strings"

	"dreamina-manager/internal/store"
)

type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return e.msg }

func errBadRequest(msg string) error { return &badRequestError{msg: msg} }

func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// overview 返回账号池总览，字段与 TS 版一致。
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	stats, err := s.DB.GetStats()
	if err != nil {
		failErr(w, err)
		return
	}
	strategy, err := s.DB.GetSetting("selection.strategy")
	if err != nil {
		failErr(w, err)
		return
	}
	if strategy == "" {
		strategy = string(store.StrategyLeastFailures)
	}

	var active any
	if stats.ActiveAccountID != "" {
		if a, err := s.DB.GetAccount(stats.ActiveAccountID); err == nil {
			active = a
		}
	}

	ok(w, map[string]any{
		"total":           stats.Total,
		"enabled":         stats.Enabled,
		"healthy":         stats.Healthy,
		"expired":         stats.Expired,
		"errorCount":      stats.ErrorCount,
		"unknown":         stats.Unknown,
		"inCooldown":      stats.InCooldown,
		"withProxy":       stats.WithProxy,
		"totalCredit":     stats.TotalCredit,
		"vipCount":        0,
		"activeAccountId": stats.ActiveAccountID,
		"probe":           s.Prober.State(),
		"strategy":        strategy,
		"activeAccount":   active,
	})
}

func (s *Server) probeAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs         []string `json:"ids"`
		Concurrency int      `json:"concurrency"`
	}
	_ = decodeJSON(r, &req) // body 可为空
	state, err := s.Prober.ProbeAll(r.Context(), req.IDs)
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, state)
}

func (s *Server) probeStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, s.Prober.State())
}

func (s *Server) pick(w http.ResponseWriter, r *http.Request) {
	strategy := store.ParseStrategy(r.URL.Query().Get("strategy"))
	if strategy == store.StrategyLeastFailures {
		if fromDB, err := s.DB.GetSetting("selection.strategy"); err == nil && fromDB != "" {
			strategy = store.ParseStrategy(fromDB)
		}
	}
	account, err := s.DB.SelectAccount(strategy, r.URL.Query().Get("provider"))
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, account)
}

func (s *Server) setStrategy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Strategy string `json:"strategy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}
	switch store.SelectionStrategy(req.Strategy) {
	case store.StrategyActive, store.StrategyLeastFailures, store.StrategyRoundRobin, store.StrategyMostCredit:
	default:
		fail(w, http.StatusBadRequest, "VALIDATION", "未知的调度策略: "+req.Strategy)
		return
	}
	if err := s.DB.SetSetting("selection.strategy", req.Strategy); err != nil {
		failErr(w, err)
		return
	}
	ok(w, map[string]any{"strategy": req.Strategy})
}

func (s *Server) globalEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.DB.RecentEvents(r.URL.Query().Get("accountId"), queryInt(r, "limit", 80))
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, events)
}

func (s *Server) bulkImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text     string `json:"text"`
		Provider string `json:"provider"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		fail(w, http.StatusBadRequest, "VALIDATION", "导入内容不能为空")
		return
	}

	type importError struct {
		Line   int    `json:"line"`
		Value  string `json:"value"`
		Reason string `json:"reason"`
	}
	created := []store.Account{}
	failed := []importError{}

	for i, line := range strings.Split(strings.ReplaceAll(req.Text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, payload := "", line
		// 支持「备注名<Tab>凭据」或「备注名,凭据」
		sep := strings.IndexAny(line, "\t,")
		if sep > 0 {
			head, rest := strings.TrimSpace(line[:sep]), strings.TrimSpace(line[sep+1:])
			if head != "" && rest != "" && !strings.ContainsAny(head, "=;") {
				name, payload = head, rest
			}
		}
		in, err := buildCreateInput(createAccountRequest{
			Name: name, SessionID: payload, Provider: req.Provider,
		})
		if err != nil {
			failed = append(failed, importError{Line: i + 1, Value: truncate(payload, 24), Reason: err.Error()})
			continue
		}
		account, err := s.DB.CreateAccount(in)
		if err != nil {
			failed = append(failed, importError{Line: i + 1, Value: truncate(payload, 24), Reason: err.Error()})
			continue
		}
		created = append(created, *account)
	}

	if len(created) > 0 {
		s.DB.LogEvent(store.Event{
			Kind: "account.bulk_import",
			Message: "批量导入成功 " + strconv.Itoa(len(created)) + " 个账号，失败 " +
				strconv.Itoa(len(failed)) + " 个",
		})
	}
	ok(w, map[string]any{
		"created":  len(created),
		"failed":   len(failed),
		"accounts": created,
		"errors":   failed,
	})
}

func (s *Server) exportAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.DB.ListAccounts()
	if err != nil {
		failErr(w, err)
		return
	}
	if r.URL.Query().Get("credentials") != "1" {
		ok(w, map[string]any{"accounts": accounts})
		return
	}

	type exported struct {
		Name         string   `json:"name"`
		Remark       string   `json:"remark"`
		Tags         []string `json:"tags"`
		Provider     string   `json:"provider"`
		Credential   string   `json:"credential"`
		ProxyURL     string   `json:"proxyUrl"`
		StoreIDC     string   `json:"storeIdc"`
		StoreCountry string   `json:"storeCountry"`
	}
	out := make([]exported, 0, len(accounts))
	for _, a := range accounts {
		secret, err := s.DB.GetSecret(a.ID)
		if err != nil {
			continue
		}
		out = append(out, exported{
			Name: a.Name, Remark: a.Remark, Tags: a.Tags,
			Provider: string(a.Provider), Credential: secret.Credential,
			ProxyURL: secret.ProxyURL, StoreIDC: a.StoreIDC, StoreCountry: a.StoreCountry,
		})
	}
	s.DB.LogEvent(store.Event{Level: "warn", Kind: "account.export", Message: "导出含明文凭据的账号备份"})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": map[string]any{"exportedAt": store.NowISO(), "accounts": out},
	})
}
