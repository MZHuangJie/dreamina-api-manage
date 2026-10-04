package api

import (
	"net/http"
	"strings"

	"dreamina-manager/internal/browser"
	"dreamina-manager/internal/crypto"
	"dreamina-manager/internal/dreamina"
	"dreamina-manager/internal/store"
)

// listAccounts 支持 keyword / health / enabled / tag 过滤。
func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.DB.ListAccounts()
	if err != nil {
		failErr(w, err)
		return
	}

	q := r.URL.Query()
	keyword := strings.ToLower(strings.TrimSpace(q.Get("keyword")))
	healthFilter := q.Get("health")
	enabledFilter := q.Get("enabled")
	tagFilter := q.Get("tag")

	out := make([]store.Account, 0, len(accounts))
	for _, a := range accounts {
		if keyword != "" {
			haystack := strings.ToLower(a.Name + " " + a.Remark + " " + a.Nickname + " " + a.UserID)
			if !strings.Contains(haystack, keyword) {
				continue
			}
		}
		if healthFilter != "" && string(a.Health) != healthFilter {
			continue
		}
		if enabledFilter == "true" && !a.Enabled {
			continue
		}
		if enabledFilter == "false" && a.Enabled {
			continue
		}
		if tagFilter != "" && !contains(a.Tags, tagFilter) {
			continue
		}
		out = append(out, a)
	}
	ok(w, out)
}

type createAccountRequest struct {
	Name         string   `json:"name"`
	Remark       string   `json:"remark"`
	Tags         []string `json:"tags"`
	Provider     string   `json:"provider"`
	Cookie       string   `json:"cookie"`
	SessionID    string   `json:"sessionId"`
	Enabled      *bool    `json:"enabled"`
	ProxyURL     string   `json:"proxyUrl"`
	ProxyEnabled *bool    `json:"proxyEnabled"`
	StoreIDC     string   `json:"storeIdc"`
	StoreCountry string   `json:"storeCountry"`
}

// buildCreateInput 把请求体翻译成 store 的入参，并补全从 Cookie 里能推断的字段。
func buildCreateInput(req createAccountRequest) (store.CreateAccountInput, error) {
	credential := strings.TrimSpace(req.Cookie)
	kind := store.KindCookie
	if credential == "" {
		credential = strings.TrimSpace(req.SessionID)
		kind = store.KindSessionID
	}
	if credential == "" {
		return store.CreateAccountInput{}, errBadRequest("必须提供 sessionId 或 cookie")
	}
	if _, err := crypto.ExtractSessionID(credential); err != nil {
		return store.CreateAccountInput{}, errBadRequest(err.Error())
	}

	prov := store.Provider(req.Provider)
	if prov == "" {
		prov = store.ProviderDreamina
	}

	// Dreamina 的集群归属藏在 cookie 里，能自动抽就自动抽
	storeIDC := req.StoreIDC
	storeCountry := req.StoreCountry
	if storeIDC == "" {
		storeIDC = crypto.ExtractCookieValue(credential, "store-idc")
	}
	if storeCountry == "" {
		storeCountry = crypto.ExtractCookieValue(credential, "store-country-code")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sessionID, _ := crypto.ExtractSessionID(credential)
		suffix := sessionID
		if len(suffix) > 6 {
			suffix = suffix[:6]
		}
		name = "账号 " + suffix
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	proxyEnabled := req.ProxyURL != ""
	if req.ProxyEnabled != nil {
		proxyEnabled = *req.ProxyEnabled && req.ProxyURL != ""
	}

	return store.CreateAccountInput{
		Name:         name,
		Remark:       req.Remark,
		Tags:         req.Tags,
		Provider:     prov,
		Credential:   credential,
		Kind:         kind,
		ProxyURL:     strings.TrimSpace(req.ProxyURL),
		ProxyEnabled: proxyEnabled,
		Enabled:      enabled,
		StoreIDC:     storeIDC,
		StoreCountry: storeCountry,
	}, nil
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}
	in, err := buildCreateInput(req)
	if err != nil {
		failErr(w, err)
		return
	}
	account, err := s.DB.CreateAccount(in)
	if err != nil {
		failErr(w, err)
		return
	}
	s.DB.LogEvent(store.Event{AccountID: account.ID, Kind: "account.create", Message: "新增账号「" + account.Name + "」"})

	// 新建后立即探活一次，让用户马上看到登录态与积分；失败不影响创建结果
	_ = s.Prober.ProbeOne(r.Context(), account.ID)

	updated, err := s.DB.GetAccount(account.ID)
	if err != nil {
		failErr(w, err)
		return
	}
	okStatus(w, http.StatusCreated, updated)
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	account, err := s.DB.GetAccount(r.PathValue("id"))
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, account)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name         *string   `json:"name"`
		Remark       *string   `json:"remark"`
		Tags         *[]string `json:"tags"`
		Cookie       *string   `json:"cookie"`
		SessionID    *string   `json:"sessionId"`
		Enabled      *bool     `json:"enabled"`
		ProxyURL     *string   `json:"proxyUrl"`
		ProxyEnabled *bool     `json:"proxyEnabled"`
		StoreIDC     *string   `json:"storeIdc"`
		StoreCountry *string   `json:"storeCountry"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}

	patch := store.AccountPatch{
		Name:         req.Name,
		Remark:       req.Remark,
		Tags:         req.Tags,
		Enabled:      req.Enabled,
		ProxyURL:     req.ProxyURL,
		ProxyEnabled: req.ProxyEnabled,
		StoreIDC:     req.StoreIDC,
		StoreCountry: req.StoreCountry,
	}
	credential := ""
	if req.Cookie != nil && strings.TrimSpace(*req.Cookie) != "" {
		credential = strings.TrimSpace(*req.Cookie)
	} else if req.SessionID != nil && strings.TrimSpace(*req.SessionID) != "" {
		credential = strings.TrimSpace(*req.SessionID)
	}
	if credential != "" {
		if _, err := crypto.ExtractSessionID(credential); err != nil {
			failErr(w, errBadRequest(err.Error()))
			return
		}
		patch.Credential = &credential
	}

	account, err := s.DB.UpdateAccount(id, patch)
	if err != nil {
		failErr(w, err)
		return
	}
	s.DB.LogEvent(store.Event{AccountID: id, Kind: "account.update", Message: "更新账号「" + account.Name + "」"})
	ok(w, account)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account, err := s.DB.GetAccount(id)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := s.DB.DeleteAccount(id); err != nil {
		failErr(w, err)
		return
	}
	s.DB.LogEvent(store.Event{Kind: "account.delete", Message: "删除账号「" + account.Name + "」"})
	ok(w, map[string]any{"deleted": true})
}

func (s *Server) setEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		fail(w, http.StatusBadRequest, "VALIDATION", "需要 enabled 字段")
		return
	}
	id := r.PathValue("id")
	if err := s.DB.SetEnabled(id, *req.Enabled); err != nil {
		failErr(w, err)
		return
	}
	account, err := s.DB.GetAccount(id)
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, account)
}

func (s *Server) activate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.DB.SetActiveAccount(id); err != nil {
		failErr(w, err)
		return
	}
	account, err := s.DB.GetAccount(id)
	if err != nil {
		failErr(w, err)
		return
	}
	s.DB.LogEvent(store.Event{AccountID: id, Kind: "account.switch", Message: "切换当前账号为「" + account.Name + "」"})
	ok(w, account)
}

func (s *Server) probeOne(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = s.Prober.ProbeOne(r.Context(), id)
	account, err := s.DB.GetAccount(id)
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, account)
}

func (s *Server) resetHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.DB.ResetHealth(id); err != nil {
		failErr(w, err)
		return
	}
	account, err := s.DB.GetAccount(id)
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, account)
}

func (s *Server) accountEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.DB.RecentEvents(r.PathValue("id"), queryInt(r, "limit", 50))
	if err != nil {
		failErr(w, err)
		return
	}
	ok(w, events)
}

// receiveCredit 尝试领取每日赠送积分。
//
// 这里**真的去调接口**，而不是预先断定有没有这个活动。
//
// 教训：这个函数原先直接返回「海外版没有每日赠送」，那是我早期凭积分流水
// 下的判断。后来从前端 JS 里挖到 /commerce/v1/benefits/credit_receive，
// 实测它返回风控拒绝（34070104 shark action check reject）——说明接口存在、
// 会话有效，只是缺少页面上下文里的签名。一个写死的「不支持」把这条路堵了很久。
func (s *Server) receiveCredit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	secret, err := s.DB.GetSecret(id)
	if err != nil {
		failErr(w, err)
		return
	}

	ctx := r.Context()
	client := s.loginClient()
	account := dreaminaAccountFor(secret)

	// 先记下领取前的余额，用来判断到底有没有真的到账
	before, _ := client.GetCreditOverview(ctx, account)
	result, claimErr := client.ClaimDailyCredit(ctx, account)
	after, _ := client.GetCreditOverview(ctx, account)

	payload := map[string]any{"accountId": id}
	if before != nil {
		payload["before"] = before.Credit.Total()
	}
	if after != nil {
		payload["after"] = after.Credit.Total()
	}
	if before != nil && after != nil {
		payload["delta"] = after.Credit.Total() - before.Credit.Total()
	}
	if len(result) > 0 {
		payload["raw"] = string(result)
	}

	if claimErr != nil {
		payload["message"] = claimErr.Error()
		// 用 200 返回：这是「一次尝试的结果」，不是服务端故障。
		// 前端需要同时看到错误原因和余额变化。
		ok(w, payload)
		return
	}
	payload["claimed"] = true
	ok(w, payload)
}

// dreaminaAccountFor 把库里的凭据转成 Dreamina 客户端用的账号。
//
// 归属地优先从凭据里现取——库里存的可能是添加账号时抽的，早就过时了。
func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func dreaminaAccountFor(secret *store.Secret) dreamina.Account {
	acct := secret.Account
	storeIDC := acct.StoreIDC
	country := acct.StoreCountry
	if idc := crypto.ExtractCookieValue(secret.Credential, "store-idc"); idc != "" {
		storeIDC = idc
	}
	if cc := crypto.ExtractCookieValue(secret.Credential, "store-country-code"); cc != "" {
		country = cc
	}

	endpoints, err := dreamina.ResolveEndpoints(storeIDC, country)
	if err != nil {
		endpoints = dreamina.Candidates(storeIDC, country)[0]
	}

	sessionID, _ := crypto.ExtractSessionID(secret.Credential)
	const domain = ".capcut.com"
	cookies := []browser.Cookie{
		{Name: "sessionid", Value: sessionID, Domain: domain, Path: "/"},
		{Name: "sessionid_ss", Value: sessionID, Domain: domain, Path: "/"},
		{Name: "sid_tt", Value: sessionID, Domain: domain, Path: "/"},
		{Name: "store-idc", Value: storeIDC, Domain: domain, Path: "/"},
		{Name: "store-country-code", Value: country, Domain: domain, Path: "/"},
	}
	if csrf := crypto.ExtractCookieValue(secret.Credential, "passport_csrf_token"); csrf != "" {
		cookies = append(cookies,
			browser.Cookie{Name: "passport_csrf_token", Value: csrf, Domain: domain, Path: "/"},
			browser.Cookie{Name: "passport_csrf_token_default", Value: csrf, Domain: domain, Path: "/"},
		)
	}

	return dreamina.Account{
		ID:        acct.ID,
		Cookies:   cookies,
		Proxy:     secret.ProxyURL,
		Endpoints: endpoints,
		Country:   country,
		StoreIDC:  storeIDC,
	}
}
