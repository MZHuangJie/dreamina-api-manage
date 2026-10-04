// Package api 提供与 TypeScript 版**契约完全一致**的 HTTP 接口。
//
// 「完全一致」是硬要求：前端不做任何改动就能从 TS 后端切到 Go 后端。
// 响应信封、字段名、状态码、错误码都逐一对齐。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dreamina-manager/internal/browser"
	"dreamina-manager/internal/gateway"
	"dreamina-manager/internal/health"
	"dreamina-manager/internal/provider"
	"dreamina-manager/internal/store"
	"dreamina-manager/internal/tasks"
)

// Server 聚合所有依赖。
type Server struct {
	DB        *store.DB
	Providers *provider.Registry
	Prober    *health.Prober
	Tasks     *tasks.Manager
	// AccessToken 非空时所有 /api 请求都要带 X-Access-Token。
	AccessToken string
	// WebDist 是前端构建产物目录，为空则不提供静态资源。
	WebDist string
	// Gateway 是聚合网关。非空时提供 OpenAI 兼容接口。
	Gateway *gateway.Server
	// Sidecar 是浏览器通道客户端。人工登录需要它开窗口。
	Sidecar *browser.Client

	// GatewayOnAdminPort 决定网关是否也挂在管理端口上。
	//
	// 桌面端需要它：管理端口本来就只绑回环，不存在暴露风险，
	// 而且界面里的 Base URL 是 location.origin + "/v1"，同端口才对得上。
	//
	// 服务端**必须保持 false**——那里管理端和网关是两个端口，
	// 混在一起就等于把管理接口和对外接口重新绑回同一个监听。
	GatewayOnAdminPort bool
}

// AdminHandler 是**管理端**路由：/api/* 与前端界面。
//
// 它和 GatewayHandler 跑在**不同的监听端口**上，这是刻意的安全设计：
// 把管理端端口绑在回环地址、只对外暴露网关端口，管理面就**物理上**不可达，
// 而不是仅仅靠一个令牌挡着。令牌可能忘了设、可能泄漏，端口不监听则无懈可击。
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"pong": true, "at": store.NowISO()})
	})

	mux.HandleFunc("GET /api/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/accounts", s.createAccount)
	mux.HandleFunc("POST /api/accounts/bulk", s.bulkImport)
	mux.HandleFunc("GET /api/accounts/export", s.exportAccounts)
	mux.HandleFunc("GET /api/accounts/{id}", s.getAccount)
	mux.HandleFunc("PATCH /api/accounts/{id}", s.updateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.deleteAccount)
	mux.HandleFunc("POST /api/accounts/{id}/enabled", s.setEnabled)
	mux.HandleFunc("POST /api/accounts/{id}/activate", s.activate)
	mux.HandleFunc("POST /api/accounts/{id}/probe", s.probeOne)
	mux.HandleFunc("POST /api/accounts/{id}/receive-credit", s.receiveCredit)
	mux.HandleFunc("POST /api/accounts/{id}/reset-health", s.resetHealth)
	mux.HandleFunc("GET /api/accounts/{id}/events", s.accountEvents)
	mux.HandleFunc("POST /api/accounts/{id}/netcheck", s.netcheckAccount)
	mux.HandleFunc("POST /api/accounts/{id}/login", s.startLogin)
	mux.HandleFunc("GET /api/accounts/{id}/login", s.loginStatus)
	mux.HandleFunc("POST /api/accounts/{id}/login/capture", s.captureLogin)
	mux.HandleFunc("DELETE /api/accounts/{id}/login", s.cancelLogin)

	mux.HandleFunc("GET /api/pool/overview", s.overview)
	mux.HandleFunc("POST /api/pool/probe-all", s.probeAll)
	mux.HandleFunc("GET /api/pool/probe-status", s.probeStatus)
	mux.HandleFunc("GET /api/pool/pick", s.pick)
	mux.HandleFunc("PUT /api/pool/strategy", s.setStrategy)
	mux.HandleFunc("GET /api/pool/events", s.globalEvents)

	// —— 聚合网关的密钥管理（管理端，走访问令牌）——
	mux.HandleFunc("GET /api/keys", s.listKeys)
	mux.HandleFunc("POST /api/keys", s.createKey)
	mux.HandleFunc("PATCH /api/keys/{id}", s.updateKey)
	mux.HandleFunc("DELETE /api/keys/{id}", s.deleteKey)

	mux.HandleFunc("GET /api/models", s.models)
	mux.HandleFunc("POST /api/generate", s.createGeneration)
	mux.HandleFunc("GET /api/generations/stats", s.generationStats)
	mux.HandleFunc("GET /api/generations", s.listGenerations)
	mux.HandleFunc("GET /api/generations/{id}", s.getGeneration)
	mux.HandleFunc("POST /api/generations/{id}/cancel", s.cancelGeneration)
	mux.HandleFunc("DELETE /api/generations/{id}", s.deleteGeneration)

	if s.GatewayOnAdminPort && s.Gateway != nil {
		mux.Handle("/v1/", s.Gateway.Handler())
	} else {
		// 网关**不在**这个端口上。显式返回 404，而不是让 SPA 兜底吐出一个
		// 200 + HTML——后者会把「反代配错了端口」变成一个静默的、很难察觉的故障：
		// 调用方拿到 200 却解析不出 JSON，排查方向全错。
		mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
			fail(w, http.StatusNotFound, "WRONG_PORT",
				"网关不在管理端口上，请访问 GATEWAY_PORT 对应的端口")
		})
	}

	if s.WebDist != "" {
		if _, err := os.Stat(filepath.Join(s.WebDist, "index.html")); err == nil {
			mux.Handle("/", s.spaHandler())
		}
	}

	return s.withAuth(mux)
}

// GatewayHandler 是**对外网关**路由：只有 /v1/*。
//
// 这里没有 /api/*，也没有前端界面——即使有人反代错了端口，
// 管理接口也不会从这个监听上漏出去。
func (s *Server) GatewayHandler() http.Handler {
	mux := http.NewServeMux()

	// 健康检查端点，**故意不鉴权**。
	//
	// 容器健康检查和负载均衡探活都需要它，而它们拿不到（也不该拿到）API Key。
	// 只回一个 ok，不暴露账号数、密钥数之类的信息。
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"ok\":true}" + "\n"))
	})

	if s.Gateway != nil {
		mux.Handle("/v1/", s.Gateway.Handler())
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "NOT_FOUND", "此端口只提供 /v1/* 接口")
	})
	return mux
}

// 说明：这里**故意没有**一个「什么都挂」的 Handler。
//
// 曾经有过，但那样很容易在生产里被误用，把管理接口和对外网关塞回同一个监听。
// 现在只有两个明确的入口：AdminHandler（管理端）和 GatewayHandler（对外）。
// 桌面端那种「单端口且只绑回环」的场景，用 GatewayOnAdminPort 表达即可。

// withAuth 校验管理端访问令牌。
//
// 令牌为空时只允许**本机回环**访问（桌面端就是这个模式）。
// 一旦请求来自外部地址又没有令牌，一律拒绝——这样即使有人把管理端
// 误绑到 0.0.0.0，也不至于把明文凭据直接摊在公网上。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		if s.AccessToken == "" {
			if isLoopback(r.RemoteAddr) {
				next.ServeHTTP(w, r)
				return
			}
			fail(w, http.StatusForbidden, "TOKEN_REQUIRED",
				"管理接口未设置访问令牌，为安全起见只允许本机访问。请设置 MANAGER_ACCESS_TOKEN")
			return
		}
		provided := r.Header.Get("X-Access-Token")
		if provided == "" {
			provided = r.URL.Query().Get("token")
		}
		if provided != s.AccessToken {
			fail(w, http.StatusUnauthorized, "UNAUTHORIZED", "访问令牌无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback 判断请求是否来自本机。
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// spaHandler 提供前端静态文件，未知路径回退到 index.html。
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.Dir(s.WebDist))
	indexPath := filepath.Join(s.WebDist, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(s.WebDist, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, indexPath)
	})
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func okStatus(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"ok": true, "data": data})
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": code, "message": message},
	})
}

// failErr 把领域错误映射成与 TS 版一致的错误码与状态码。
func failErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "ACCOUNT", err.Error())
	case errors.Is(err, store.ErrDuplicate):
		fail(w, http.StatusConflict, "ACCOUNT", err.Error())
	case provider.IsAuthError(err):
		fail(w, http.StatusUnauthorized, "AUTH_EXPIRED", err.Error())
	case provider.AsInsufficientCredit(err):
		fail(w, http.StatusPaymentRequired, "INSUFFICIENT_CREDIT", err.Error())
	default:
		fail(w, http.StatusBadRequest, "ACCOUNT", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("请求体不是合法 JSON")
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
