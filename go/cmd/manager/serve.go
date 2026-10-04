package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dreamina-manager/internal/api"
	"dreamina-manager/internal/dreamina"
	"dreamina-manager/internal/gateway"
	"dreamina-manager/internal/health"
	"dreamina-manager/internal/provider"
	"dreamina-manager/internal/store"
	"dreamina-manager/internal/tasks"
)

// runServe 启动完整的 HTTP 服务。
func runServe() error {
	host := env("MANAGER_HOST", "127.0.0.1")
	port := env("MANAGER_PORT", "8787")
	accessToken := os.Getenv("MANAGER_ACCESS_TOKEN")

	// 网关是**独立监听**。默认关闭，只有显式配了端口才开。
	//
	// 这是刻意的：管理端和网关共用一个端口的话，一旦有人把那个端口
	// 反代出去，管理接口（含明文凭据导出）就跟着一起暴露了。
	// 拆成两个端口之后，「对外」和「对内」是物理隔离的。
	gatewayHost := env("GATEWAY_HOST", "0.0.0.0")
	gatewayPort := os.Getenv("GATEWAY_PORT")

	// 管理端绑到非回环地址时必须设令牌，否则拒绝启动。
	//
	// 宁可起不来，也不能让一个「明文凭据导出接口」无声地挂在公网上。
	if !isLoopbackHost(host) && accessToken == "" {
		return fmt.Errorf("MANAGER_HOST=%s 监听在非本机地址，但未设置 MANAGER_ACCESS_TOKEN；"+
			"管理接口可以导出账号明文凭据，绝不能无令牌暴露。"+
			"请设置 MANAGER_ACCESS_TOKEN，或把管理端留在 127.0.0.1（推荐，只对外暴露 GATEWAY_PORT）", host)
	}

	dir := dataDir()

	// 可选：从 regions.json 补充集群定义。
	//
	// 新区域出现时不必改代码重新编译——把主机名写进这个文件就行。
	// 即使不写，客户端的自动探测也会对着内置候选表试出能用的那个。
	regionsFile := env("MANAGER_REGIONS_FILE", filepath.Join(dir, "regions.json"))
	if err := dreamina.LoadExtraClusters(regionsFile); err != nil {
		return err
	}

	key, err := loadKey(filepath.Join(dir, "secret.key"))
	if err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(dir, "manager.db"), key)
	if err != nil {
		return err
	}
	defer db.Close()

	// —— 平台适配器 ——
	registry := provider.NewRegistry()
	registry.Register(provider.NewDreamina(dreamina.NewClient(sidecarClient())))

	// —— 后台任务 ——
	maxConcurrent := envInt("MANAGER_MAX_CONCURRENT", tasks.DefaultMaxConcurrent)
	taskManager := tasks.NewManagerWithOptions(db, registry, tasks.Options{
		MaxAttempts:   envInt("MANAGER_MAX_ATTEMPTS", tasks.DefaultMaxAttempts),
		Cooldown:      time.Duration(envInt("MANAGER_FAILURE_COOLDOWN_MS", 60000)) * time.Millisecond,
		MaxConcurrent: maxConcurrent,
		WaitTimeout:   time.Duration(envInt("MANAGER_QUEUE_WAIT_MS", 30000)) * time.Millisecond,
	})
	if n, err := taskManager.ReconcileInterrupted(); err == nil && n > 0 {
		fmt.Println("已清理", n, "条上次中断的生成任务")
	}

	// —— 保活巡检 ——
	prober := health.New(db, registry, health.Options{
		Concurrency: envInt("MANAGER_HEALTH_CONCURRENCY", 3),
		Cooldown:    time.Duration(envInt("MANAGER_FAILURE_COOLDOWN_MS", 60000)) * time.Millisecond,
	})

	webDist := env("MANAGER_WEB_DIST", filepath.Join("..", "dist", "web"))
	if abs, err := filepath.Abs(webDist); err == nil {
		webDist = abs
	}

	// —— 聚合网关 ——
	//
	// 对外提供 OpenAI 兼容接口，让外部程序用 baseURL + API Key 使用账号池。
	gatewayServer := &gateway.Server{
		DB:    db,
		Tasks: taskManager,
		Models: func() []string {
			ids := []string{}
			if adapter, err := registry.For("dreamina"); err == nil {
				if list, err := adapter.Models(context.Background(), provider.Account{}); err == nil {
					for _, m := range list {
						ids = append(ids, m.ID)
					}
				}
			}
			return ids
		},
		WaitTimeout: time.Duration(envInt("GATEWAY_WAIT_TIMEOUT_MS", 180000)) * time.Millisecond,
		Limiter:     gateway.NewLimiter(envInt("GATEWAY_MAX_CONCURRENT", maxConcurrent)),
	}

	// 桌面端（没配 GATEWAY_PORT）把网关挂在管理端口上：那个端口只绑回环，
	// 没有暴露风险，而且界面里的 Base URL 就是 location.origin + "/v1"。
	// 服务端配了独立端口，则严格分开。
	gatewayOnAdmin := gatewayPort == "" && isLoopbackHost(host)

	server := &api.Server{
		DB: db, Providers: registry, Prober: prober, Tasks: taskManager,
		AccessToken: accessToken, WebDist: webDist, Gateway: gatewayServer,
		GatewayOnAdminPort: gatewayOnAdmin,
		Sidecar:            sidecarClient(),
	}

	adminServer := &http.Server{
		Addr:              host + ":" + port,
		Handler:           server.AdminHandler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	// 网关监听按需启动
	var gatewayServerHTTP *http.Server
	if gatewayPort != "" {
		gatewayServerHTTP = &http.Server{
			Addr:              gatewayHost + ":" + gatewayPort,
			Handler:           server.GatewayHandler(),
			ReadHeaderTimeout: 15 * time.Second,
		}
	}

	// —— 保活调度器 ——
	var scheduler *health.Scheduler
	if os.Getenv("MANAGER_HEALTH_ENABLED") != "false" {
		scheduler = health.NewScheduler(prober,
			time.Duration(envInt("MANAGER_HEALTH_INTERVAL_MS", 1800000))*time.Millisecond,
			time.Duration(envInt("MANAGER_HEALTH_STARTUP_DELAY_MS", 5000))*time.Millisecond)
		scheduler.Start()
	}

	// sidecar 连通性（不阻塞启动）
	sidecarState := "未连接"
	if info, err := sidecarClient().Health(context.Background()); err == nil {
		if flag, _ := info["ok"].(bool); flag {
			sidecarState = "已连接"
		}
	}

	displayHost := host
	if host == "0.0.0.0" {
		displayHost = "127.0.0.1"
	}
	fmt.Println()
	fmt.Println("  Dreamina 账号管理器（Go 核心）")
	fmt.Println("  ├─ 服务地址   http://" + displayHost + ":" + port)
	fmt.Println("  ├─ 数据目录   " + dir)
	fmt.Println("  ├─ 密钥文件   " + filepath.Join(dir, "secret.key"))
	fmt.Println("  ├─ 前端产物   " + webDist)
	fmt.Println("  ├─ 浏览器通道 " + sidecarState + " (" + env("SIDECAR_URL", "http://127.0.0.1:8790") + ")")
	fmt.Println("  ├─ 保活调度   " + boolLabel(scheduler != nil, "启用", "已关闭"))
	fmt.Println("  ├─ 平台适配   " + joinNames(registry.Names()))
	fmt.Println("  └─ 访问令牌   " + boolLabel(accessToken != "", "已启用", "未设置（仅限本机访问）"))
	if keyCount, err := db.CountAPIKeys(); err == nil {
		fmt.Println()
		fmt.Println("  聚合网关（OpenAI 兼容）")
		if gatewayPort != "" {
			fmt.Println("  ├─ 监听端口   " + gatewayHost + ":" + gatewayPort + "（只提供 /v1/*）")
		} else if gatewayOnAdmin {
			fmt.Println("  ├─ 监听端口   " + displayHost + ":" + port + "（与管理端同端口，仅回环）")
		} else {
			fmt.Println("  ├─ 监听端口   未启用（设 GATEWAY_PORT 后独立监听）")
		}
		fmt.Println("  ├─ 鉴权方式   Authorization: Bearer sk-dm-…")
		fmt.Println("  ├─ 区域集群   " + itoa(len(dreamina.Candidates("", ""))) + " 个候选（自动探测）")
		fmt.Println("  ├─ 并发生成   " + itoa(maxConcurrent))
		fmt.Println("  └─ 已发密钥   " + itoa64(keyCount) + " 把（在管理界面「接入」页管理）")
	}
	fmt.Println()

	db.LogEvent(store.Event{Kind: "server.start", Message: "Go 核心服务启动于 " + host + ":" + port})

	errCh := make(chan error, 2)
	go func() {
		if err := adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if gatewayServerHTTP != nil {
		go func() {
			if err := gatewayServerHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("网关监听失败: %w", err)
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		fmt.Println("收到", sig, "，正在关闭…")
	}

	if scheduler != nil {
		scheduler.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = adminServer.Shutdown(ctx)
	if gatewayServerHTTP != nil {
		_ = gatewayServerHTTP.Shutdown(ctx)
	}
	dreamina.CloseTransports()
	return nil
}

// isLoopbackHost 判断监听地址是否只对本机可见。
func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func boolLabel(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	if out == "" {
		return "（无）"
	}
	return out
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	if n == 0 {
		return fallback
	}
	return n
}
