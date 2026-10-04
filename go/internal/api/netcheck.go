package api

import (
	"net/http"

	"dreamina-manager/internal/netcheck"
)

// netcheckAccount 体检一个账号的出口 IP 是否与它的平台归属地一致。
//
// 存在的意义：账号归属地是注册时定死的，换 VPN 节点不会改变它；
// 但平台会看请求从哪来。这个接口把「我换了节点还能用吗」变成一个可查的问题。
func (s *Server) netcheckAccount(w http.ResponseWriter, r *http.Request) {
	secret, err := s.DB.GetSecret(r.PathValue("id"))
	if err != nil {
		failErr(w, err)
		return
	}

	exit, err := netcheck.Check(r.Context(), secret.ProxyURL)
	if err != nil {
		fail(w, http.StatusBadGateway, "NETCHECK_FAILED", err.Error())
		return
	}

	// 判断「是否经过代理」要用最终生效的那个，而不是账号自己配的那个——
	// 走系统代理时账号的 ProxyURL 是空的，但流量确实经过了代理
	effective := netcheck.ResolveProxy(secret.ProxyURL)
	result := netcheck.Compare(exit, secret.Account.StoreCountry, effective != "")
	ok(w, result)
}
