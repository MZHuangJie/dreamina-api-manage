package api

import (
	"net/http"

	"dreamina-manager/internal/dreamina"
	"dreamina-manager/internal/store"
)

// loginClient 构造一个只用于登录的 Dreamina 客户端。
func (s *Server) loginClient() *dreamina.Client {
	return dreamina.NewClient(s.Sidecar)
}

// startLogin 打开一个可见的浏览器窗口，让用户手动登录。
//
// 界面上的「重新登录」按钮调它。之所以不做账密直登：图形验证码、
// 邮箱验证码、Google 登录这几样逆向成本极高且容易触发风控，
// 而把真实浏览器交给用户，这些全都天然支持。
func (s *Server) startLogin(w http.ResponseWriter, r *http.Request) {
	secret, err := s.DB.GetSecret(r.PathValue("id"))
	if err != nil {
		failErr(w, err)
		return
	}
	if err := s.loginClient().StartLogin(r.Context(), secret.Account.ID, secret.ProxyURL); err != nil {
		fail(w, http.StatusBadGateway, "LOGIN_WINDOW_FAILED", err.Error())
		return
	}
	s.DB.LogEvent(store.Event{
		AccountID: secret.Account.ID, Kind: "account.login_start",
		Message: "已打开登录窗口，等待用户完成登录",
	})
	ok(w, map[string]any{"started": true, "accountId": secret.Account.ID})
}

// loginStatus 查询登录窗口的状态。
func (s *Server) loginStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.loginClient().PollLogin(r.Context(), id)
	if err != nil {
		fail(w, http.StatusBadGateway, "LOGIN_POLL_FAILED", err.Error())
		return
	}
	ok(w, session)
}

// captureLogin 把登录窗口里的凭据保存下来并关掉窗口。
func (s *Server) captureLogin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client := s.loginClient()

	session, err := client.PollLogin(r.Context(), id)
	if err != nil {
		fail(w, http.StatusBadGateway, "LOGIN_POLL_FAILED", err.Error())
		return
	}
	if !session.LoggedIn || session.Credential == "" {
		fail(w, http.StatusBadRequest, "NOT_LOGGED_IN", "还没有检测到登录态，请先在窗口里完成登录")
		return
	}

	credential := session.Credential
	storeIDC := session.StoreIDC
	storeCountry := session.StoreCountry
	account, err := s.DB.UpdateAccount(id, store.AccountPatch{
		Credential:   &credential,
		StoreIDC:     &storeIDC,
		StoreCountry: &storeCountry,
	})
	if err != nil {
		failErr(w, err)
		return
	}

	_ = client.CloseLogin(r.Context(), id)
	s.DB.LogEvent(store.Event{
		AccountID: id, Kind: "account.relogin",
		Message: "通过管理器窗口重新登录并更新了凭据",
	})

	// 登录完立刻探活一次，让界面马上显示新的积分与状态
	_ = s.Prober.ProbeOne(r.Context(), id)
	if refreshed, err := s.DB.GetAccount(id); err == nil {
		account = refreshed
	}
	ok(w, account)
}

// cancelLogin 关掉登录窗口（用户点取消时）。
func (s *Server) cancelLogin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.loginClient().CloseLogin(r.Context(), id); err != nil {
		fail(w, http.StatusBadGateway, "LOGIN_CLOSE_FAILED", err.Error())
		return
	}
	ok(w, map[string]any{"closed": true})
}
