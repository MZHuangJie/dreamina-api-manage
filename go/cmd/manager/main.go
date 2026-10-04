// Command manager 是 Dreamina 账号管理器的核心。
//
// 架构：Go 负责账号池、调度、存储与全部接口调用；
// 浏览器通道（sidecar）只负责在已登录页面上下文里代发写请求。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"dreamina-manager/internal/browser"
	"dreamina-manager/internal/crypto"
	"dreamina-manager/internal/dreamina"
	"dreamina-manager/internal/netcheck"
	"dreamina-manager/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd, rest := os.Args[1], os.Args[2:]

	switch cmd {
	case "serve":
		must(runServe())
	case "account":
		must(runAccount(rest))
	case "credit":
		must(runCredit(rest))
	case "timeline", "tl":
		must(runTimeline(rest))
	case "claim":
		must(runClaim(rest))
	case "history":
		must(runCreditHistory(rest))
	case "gen":
		must(runGenerate(rest))
	case "sidecar":
		must(runSidecarHealth())
	case "login":
		must(runLogin(rest))
	case "netcheck", "net":
		must(runNetcheck(rest))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "未知命令:", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法:
  manager serve                                  启动 HTTP 服务
  manager sidecar                                检查 sidecar 健康状态
  manager account add <名称> <凭据> [--proxy url]  添加账号
  manager account list                           列出账号
  manager account rm <id|名称>                   删除账号
  manager account use <id|名称>                  切换当前账号
  manager credit [id|名称]                       查询积分
  manager gen <提示词> [id|名称]                 端到端生成一张图
  manager login [id|名称] [--new 名称]          在浏览器窗口里登录并自动保存凭据
  manager netcheck [id|名称]                     体检出口 IP 与账号归属地是否一致
  manager history [id|名称]                      积分流水（平台侧的收支明细）
  manager timeline [id|名称]                     积分时间线（本机采样，看每日是否到账）
  manager claim [id|名称]                        尝试领取每日赠送积分

环境变量:
  MANAGER_DATA_DIR   数据目录（默认 data）
  MANAGER_SECRET     加密密钥（优先于 data/secret.key）
  SIDECAR_URL        sidecar 地址（默认 http://127.0.0.1:8790）
  SIDECAR_SECRET     sidecar 共享密钥
`)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func dataDir() string { return env("MANAGER_DATA_DIR", "data") }

// openStore 打开数据库与密钥。
func openStore() (*store.DB, error) {
	dir := dataDir()
	key, err := crypto.LoadOrCreateKey(filepath.Join(dir, "secret.key"), os.Getenv("MANAGER_SECRET"))
	if err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dir, "manager.db"), key)
}

func sidecarClient() *browser.Client {
	return browser.New(env("SIDECAR_URL", "http://127.0.0.1:8790"), os.Getenv("SIDECAR_SECRET"))
}

/* ------------------------------ account ------------------------------ */

func runAccount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: manager account add|list|rm|use")
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "add":
		return accountAdd(db, args[1:])
	case "list", "ls":
		return accountList(db)
	case "rm", "del", "delete":
		if len(args) < 2 {
			return fmt.Errorf("用法: manager account rm <id|名称>")
		}
		id, err := resolveAccount(db, args[1])
		if err != nil {
			return err
		}
		if err := db.DeleteAccount(id); err != nil {
			return err
		}
		db.LogEvent(store.Event{Kind: "account.delete", Message: "删除账号 " + args[1]})
		fmt.Println("已删除", args[1])
		return nil
	case "use", "activate":
		if len(args) < 2 {
			return fmt.Errorf("用法: manager account use <id|名称>")
		}
		id, err := resolveAccount(db, args[1])
		if err != nil {
			return err
		}
		if err := db.SetActiveAccount(id); err != nil {
			return err
		}
		db.LogEvent(store.Event{Kind: "account.switch", Message: "切换当前账号为 " + args[1]})
		fmt.Println("已切换到", args[1])
		return nil
	default:
		return fmt.Errorf("未知子命令: %s", args[0])
	}
}

func accountAdd(db *store.DB, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("用法: manager account add <名称> <凭据> [--proxy url]")
	}
	in := store.CreateAccountInput{
		Name:       args[0],
		Credential: args[1],
		Provider:   store.ProviderDreamina,
		Kind:       store.KindSessionID,
		Enabled:    true,
	}
	for i := 2; i < len(args); i++ {
		if args[i] == "--proxy" && i+1 < len(args) {
			in.ProxyURL = args[i+1]
			in.ProxyEnabled = true
			i++
		}
	}

	// 从凭据里解析 sessionid 做校验，并抽出集群归属
	if _, err := crypto.ExtractSessionID(in.Credential); err != nil {
		return err
	}
	in.StoreIDC = crypto.ExtractCookieValue(in.Credential, "store-idc")
	in.StoreCountry = crypto.ExtractCookieValue(in.Credential, "store-country-code")

	a, err := db.CreateAccount(in)
	if err != nil {
		return err
	}
	db.LogEvent(store.Event{AccountID: a.ID, Kind: "account.create", Message: "新增账号 " + a.Name})
	fmt.Printf("已添加 %s（id=%s，平台=%s，集群=%s/%s）\n", a.Name, a.ID[:8], a.Provider, a.StoreCountry, a.StoreIDC)
	return nil
}

func accountList(db *store.DB) error {
	accounts, err := db.ListAccounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Println("（还没有账号）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "当前\t名称\t平台\t状态\t积分\t集群\tid")
	for _, a := range accounts {
		mark := ""
		if a.IsActive {
			mark = "*"
		}
		credit := "-"
		if a.TotalCredit != nil {
			credit = fmt.Sprintf("%d", *a.TotalCredit)
		}
		idc := a.StoreIDC
		if idc == "" {
			idc = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			mark, a.Name, a.Provider, a.Health, credit, idc, a.ID[:8])
	}
	return w.Flush()
}

// resolveAccount 支持用 id 前缀或名称定位账号。
func resolveAccount(db *store.DB, ref string) (string, error) {
	accounts, err := db.ListAccounts()
	if err != nil {
		return "", err
	}
	for _, a := range accounts {
		if a.Name == ref || a.ID == ref || strings.HasPrefix(a.ID, ref) {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("找不到账号: %s", ref)
}

/* ------------------------------ Dreamina ------------------------------ */

// dreaminaAccount 从库里取一个账号，组装成请求上下文。
func dreaminaAccount(db *store.DB, ref string) (dreamina.Account, error) {
	accounts, err := db.ListAccounts()
	if err != nil {
		return dreamina.Account{}, err
	}
	var target *store.Account
	for i := range accounts {
		a := accounts[i]
		if ref == "" && a.IsActive {
			target = &a
			break
		}
		if ref == "" && target == nil {
			target = &a
		}
		if ref != "" && (a.Name == ref || a.ID == ref || strings.HasPrefix(a.ID, ref)) {
			target = &a
			break
		}
	}
	if target == nil {
		return dreamina.Account{}, fmt.Errorf("没有可用的账号，请先 manager account add")
	}

	secret, err := db.GetSecret(target.ID)
	if err != nil {
		return dreamina.Account{}, err
	}
	endpoints, err := dreamina.ResolveEndpoints(target.StoreIDC, target.StoreCountry)
	if err != nil {
		return dreamina.Account{}, err
	}

	const domain = ".capcut.com"
	sessionID, err := crypto.ExtractSessionID(secret.Credential)
	if err != nil {
		return dreamina.Account{}, err
	}
	cookies := []browser.Cookie{
		{Name: "sessionid", Value: sessionID, Domain: domain, Path: "/"},
		{Name: "sessionid_ss", Value: sessionID, Domain: domain, Path: "/"},
		{Name: "sid_tt", Value: sessionID, Domain: domain, Path: "/"},
		{Name: "store-idc", Value: target.StoreIDC, Domain: domain, Path: "/"},
		{Name: "store-country-code", Value: target.StoreCountry, Domain: domain, Path: "/"},
	}
	if csrf := crypto.ExtractCookieValue(secret.Credential, "passport_csrf_token"); csrf != "" {
		cookies = append(cookies, browser.Cookie{Name: "passport_csrf_token", Value: csrf, Domain: domain, Path: "/"})
	}

	return dreamina.Account{
		ID:        "acct-" + target.ID[:8],
		Cookies:   cookies,
		Proxy:     secret.ProxyURL,
		Endpoints: endpoints,
	}, nil
}

/* ------------------------------ 命令实现 ------------------------------ */

// netcheckSecret 按 id / 名称前缀定位账号并取出凭据。
func netcheckSecret(db *store.DB, ref string) (*store.Secret, error) {
	accounts, err := db.ListAccounts()
	if err != nil {
		return nil, err
	}
	var target *store.Account
	for i := range accounts {
		a := accounts[i]
		if ref == "" {
			// 不指定就用当前账号；没有当前账号则用第一个
			if a.IsActive {
				target = &a
				break
			}
			if target == nil {
				target = &a
			}
			continue
		}
		if a.Name == ref || a.ID == ref || strings.HasPrefix(a.ID, ref) {
			target = &a
			break
		}
	}
	if target == nil {
		if ref == "" {
			return nil, fmt.Errorf("还没有任何账号")
		}
		return nil, fmt.Errorf("找不到账号: %s", ref)
	}
	return db.GetSecret(target.ID)
}

// runNetcheck 体检账号出口 IP 与平台归属地是否一致。
func runNetcheck(args []string) error {
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	secret, err := netcheckSecret(db, ref)
	if err != nil {
		return err
	}

	// 实际生效的出口：账号代理 > 系统代理 > 直连
	proxy := netcheck.ResolveProxy(secret.ProxyURL)
	label := "直连（本机出口）"
	switch {
	case secret.ProxyURL != "":
		label = secret.ProxyURL + "（账号代理）"
	case proxy != "":
		label = proxy + "（系统代理，由 VPN 设置）"
	}
	fmt.Println("正在通过 " + label + " 查询出口 IP…")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exit, err := netcheck.Check(ctx, secret.ProxyURL)
	if err != nil {
		return err
	}
	result := netcheck.Compare(exit, secret.Account.StoreCountry, proxy != "")

	fmt.Println()
	fmt.Println("  账号        " + secret.Account.Name)
	fmt.Println("  平台归属地  " + strings.ToUpper(defaultIfEmpty(secret.Account.StoreCountry, "（未记录）")))
	switch {
	case secret.ProxyURL != "":
		fmt.Println("  出口代理    " + secret.ProxyURL + "（账号专属）")
	case proxy != "":
		fmt.Println("  出口代理    " + proxy + "（跟随系统代理 / VPN）")
	default:
		fmt.Println("  出口代理    未启用（直连）")
	}
	fmt.Println()
	fmt.Println("  出口 IP     " + exit.IP)
	fmt.Println("  出口地区    " + exit.CountryName + " " + strings.ToUpper(exit.Country))
	if exit.ISP != "" {
		fmt.Println("  运营商      " + exit.ISP)
	}
	fmt.Println("  延迟        " + itoa(int(exit.LatencyMS)) + " ms（到 " + exit.Source + "）")
	fmt.Println()
	if result.Match {
		fmt.Println("  ✅ " + result.Verdict)
	} else {
		fmt.Println("  ⚠️  " + result.Verdict)
		if result.Advice != "" {
			fmt.Println("      " + result.Advice)
		}
	}
	return nil
}

func defaultIfEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func runSidecarHealth() error {
	info, err := sidecarClient().Health(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("sidecar 正常: %v\n", info)
	return nil
}

func runCredit(args []string) error {
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	account, err := dreaminaAccount(db, ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	credit, err := dreamina.NewClient(sidecarClient()).GetCredit(ctx, account)
	if err != nil {
		return err
	}
	fmt.Printf("集群: %s (%s)\n", account.Endpoints.Region, account.Endpoints.API)
	fmt.Printf("积分: 总计 %d（赠送 %d / 购买 %d / 会员 %d）\n",
		credit.Total(), credit.GiftCredit, credit.PurchaseCredit, credit.VipCredit)
	return nil
}

func runGenerate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: manager gen <提示词> [id|名称]")
	}
	prompt := args[0]
	ref := ""
	if len(args) > 1 {
		ref = args[1]
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	account, err := dreaminaAccount(db, ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := dreamina.NewClient(sidecarClient())

	fmt.Printf("集群 %s → %s\n", account.Endpoints.Region, account.Endpoints.API)

	ids, err := client.ListWorkspaces(ctx, account)
	if err != nil {
		return fmt.Errorf("取 workspace 失败: %w", err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("该账号没有任何 workspace，请先在网页端使用一次")
	}

	fmt.Println("提交中…（经浏览器通道）")
	submit, err := client.SubmitImage(ctx, account, dreamina.GenerateImageInput{
		Prompt:      prompt,
		Model:       dreamina.SeedreamV50,
		WorkspaceID: ids[0],
	})
	if err != nil {
		return fmt.Errorf("提交失败: %w", err)
	}
	fmt.Printf("已受理 history_record_id=%s\n", submit.HistoryID)

	result, err := client.PollImage(ctx, account, submit.HistoryID, 5*time.Minute,
		func(attempt, status int) { fmt.Printf("  第 %d 次查询 status=%d\n", attempt, status) })
	if err != nil {
		return fmt.Errorf("轮询失败: %w", err)
	}

	fmt.Printf("\n✅ 生成完成，共 %d 张：\n", len(result.URLs))
	for i, u := range result.URLs {
		fmt.Printf("  [%d] %s\n", i+1, u)
	}
	return nil
}

// runCreditHistory 打印积分流水。
//
// 这是回答「积分怎么没变 / 怎么少了」最直接的工具。
func runCreditHistory(args []string) error {
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	account, err := dreaminaAccount(db, ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := dreamina.NewClient(sidecarClient())

	overview, err := client.GetCreditOverview(ctx, account)
	if err == nil {
		fmt.Println("=== 当前积分 ===")
		fmt.Printf("  总计 %d（赠送 %d / 购买 %d / 会员 %d）", overview.Credit.Total(),
			overview.Credit.GiftCredit, overview.Credit.PurchaseCredit, overview.Credit.VipCredit)
		fmt.Println()
		if len(overview.Grants) > 0 {
			fmt.Println("  各笔额度的有效期：")
			for _, g := range overview.Grants {
				fmt.Printf("    剩余 %d，到期 %s", g.ResidualCredits,
					time.Unix(g.LifeEnd, 0).Format("2006-01-02"))
				fmt.Println()
			}
		}
		fmt.Println()
	}

	records, err := client.CreditHistory(ctx, account, 50)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("（没有流水记录）")
		return nil
	}

	var gained, spent int64
	fmt.Println("=== 积分流水 ===")
	for _, r := range records {
		delta := r.Delta()
		if delta > 0 {
			gained += delta
		} else {
			spent += delta
		}
		fmt.Printf("  %s  %+6d  %s", time.Unix(r.CreateTime, 0).Format("2006-01-02 15:04"), delta, r.Title)
		fmt.Println()
	}
	fmt.Println()
	fmt.Printf("  累计获得 %d，累计消耗 %d", gained, spent)
	fmt.Println()
	return nil
}

// runTimeline 打印积分时间线。
//
// 这是回答「每日赠送到底有没有到账」的核心工具：
// 它列出余额变化的时间点，而不是只看当前值。
func runTimeline(args []string) error {
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	accounts, err := db.ListAccounts()
	if err != nil {
		return err
	}
	var target *store.Account
	for i := range accounts {
		a := accounts[i]
		if ref == "" {
			if a.IsActive {
				target = &a
				break
			}
			if target == nil {
				target = &a
			}
			continue
		}
		if a.Name == ref || a.ID == ref || strings.HasPrefix(a.ID, ref) {
			target = &a
			break
		}
	}
	if target == nil {
		return fmt.Errorf("找不到账号")
	}

	deltas, err := db.CreditDeltas(target.ID, 300)
	if err != nil {
		return err
	}
	fmt.Println("账号：" + target.Name)
	fmt.Println()
	if len(deltas) == 0 {
		fmt.Println("（还没有余额变化记录。每次探活会自动采样，" +
			"也可以执行 `manager probe` 强制采样一次。）")
		return nil
	}
	fmt.Println("=== 余额变化时间线 ===")
	for _, d := range deltas {
		ts := d.To.CreatedAt
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			ts = t.Local().Format("2006-01-02 15:04")
		}
		fmt.Printf("  %s   %+6d   → %d\n", ts, d.Delta, d.To.Total)
	}
	fmt.Println()
	fmt.Println("提示：如果连续多天只有消耗、没有增加，说明该账号没有收到每日赠送。")
	return nil
}

// runClaim 尝试领取每日赠送积分。
//
// 海外版是否真有每日领取，官方文档说法不一（有的写 120 积分/天，
// 有的写 150/225 令牌/天）。这个命令直接打接口，用结果说话。
func runClaim(args []string) error {
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	account, err := dreaminaAccount(db, ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := dreamina.NewClient(sidecarClient())

	before, err := client.GetCreditOverview(ctx, account)
	if err != nil {
		return fmt.Errorf("读取余额失败（先确认登录态）: %w", err)
	}
	fmt.Printf("领取前余额：%d\n", before.Credit.Total())
	fmt.Println()

	fmt.Println("正在请求 /commerce/v1/benefits/credit_receive …")
	result, err := client.ClaimDailyCredit(ctx, account)
	if err != nil {
		fmt.Println("  接口返回：" + err.Error())
		fmt.Println()
		fmt.Println("可能的原因：")
		fmt.Println("  · 今天已经领过了")
		fmt.Println("  · 该账号/地区没有这个活动")
		fmt.Println("  · 接口还需要别的参数")
		return nil
	}
	fmt.Println("  原始返回：" + string(result))

	after, err := client.GetCreditOverview(ctx, account)
	if err == nil {
		fmt.Println()
		fmt.Printf("领取后余额：%d（变化 %+d）\n", after.Credit.Total(),
			after.Credit.Total()-before.Credit.Total())
	}
	return nil
}

// runLogin 打开一个真实浏览器窗口让用户登录，然后自动保存凭据。
//
// 这是「在管理器里登录」的入口。之所以不自己实现账密提交：
// 图形验证码、邮箱验证码、Google 登录这几样逆向成本极高且容易触发风控，
// 而把真实浏览器窗口交给用户，这些全都天然支持。
func runLogin(args []string) error {
	var (
		ref     string
		newName string
		timeout = 10 * time.Minute
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--new":
			if i+1 < len(args) {
				newName = args[i+1]
				i++
			}
		case "--timeout":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					timeout = d
				}
				i++
			}
		default:
			if ref == "" {
				ref = args[i]
			}
		}
	}

	db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	// 定位或占位一个账号
	var accountID, accountName string
	var isNew bool
	if newName != "" {
		// 新账号：先用临时 id 开窗口，登录成功后再落库——
		// 避免在库里留下一条永远没有有效凭据的记录
		accountID = "new-" + fmt.Sprint(time.Now().UnixNano())
		accountName = newName
		isNew = true
	} else {
		accounts, err := db.ListAccounts()
		if err != nil {
			return err
		}
		for _, a := range accounts {
			if ref == "" || a.Name == ref || a.ID == ref || strings.HasPrefix(a.ID, ref) {
				accountID = a.ID
				accountName = a.Name
				break
			}
		}
		if accountID == "" {
			return fmt.Errorf("找不到账号 %q；要新建请用 manager login --new <名称>", ref)
		}
	}

	client := dreamina.NewClient(sidecarClient())
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Minute)
	defer cancel()

	// 现有账号沿用它的代理，新账号先走系统代理
	proxy := ""
	if !isNew {
		if secret, err := db.GetSecret(accountID); err == nil {
			proxy = secret.ProxyURL
		}
	}

	fmt.Println("正在打开登录窗口…")
	if err := client.StartLogin(ctx, accountID, proxy); err != nil {
		return fmt.Errorf("打开登录窗口失败（浏览器通道起了吗？）: %w", err)
	}

	fmt.Println()
	fmt.Println("  一个浏览器窗口已经弹出，请在其中完成登录。")
	fmt.Println("  账密、邮箱验证码、Google 登录都可以——就是正常登录一次。")
	fmt.Println("  登录成功后会**自动**保存凭据，不用手动复制 cookie。")
	fmt.Println()

	lastPrint := time.Now()
	session, err := client.WaitForLogin(ctx, accountID, timeout, 3*time.Second,
		func(remaining time.Duration) {
			if time.Since(lastPrint) < 20*time.Second {
				return
			}
			lastPrint = time.Now()
			fmt.Printf("  等待中… 还剩 %s\n", remaining.Round(time.Second))
		})
	if err != nil {
		_ = client.CloseLogin(context.Background(), accountID)
		return err
	}

	fmt.Println()
	fmt.Println("  登录成功！")
	fmt.Println("    sessionid: " + maskSecret(session.SessionID))
	fmt.Println("    区域集群 : " + strings.ToUpper(defaultIfEmpty(session.StoreCountry, "未知")) +
		" / " + defaultIfEmpty(session.StoreIDC, "未知"))

	// 落库
	if isNew {
		created, err := db.CreateAccount(store.CreateAccountInput{
			Name:         accountName,
			Credential:   session.Credential,
			Provider:     store.ProviderDreamina,
			Kind:         store.KindCookie,
			Enabled:      true,
			StoreIDC:     session.StoreIDC,
			StoreCountry: session.StoreCountry,
		})
		if err != nil {
			return fmt.Errorf("保存账号失败: %w", err)
		}
		fmt.Println("    已新建账号：" + created.Name)
	} else {
		credential := session.Credential
		storeIDC := session.StoreIDC
		storeCountry := session.StoreCountry
		if _, err := db.UpdateAccount(accountID, store.AccountPatch{
			Credential:   &credential,
			StoreIDC:     &storeIDC,
			StoreCountry: &storeCountry,
		}); err != nil {
			return fmt.Errorf("更新凭据失败: %w", err)
		}
		db.LogEvent(store.Event{
			AccountID: accountID, Kind: "account.relogin",
			Message: "通过管理器窗口重新登录并更新了凭据",
		})
		fmt.Println("    已更新账号：" + accountName)
	}

	_ = client.CloseLogin(context.Background(), accountID)
	fmt.Println()
	fmt.Println("  接下来可以执行 manager probe 验证登录态。")
	return nil
}

// maskSecret 把敏感值打码，只留头尾便于辨认。
func maskSecret(v string) string {
	if len(v) <= 10 {
		return "***"
	}
	return v[:6] + "…" + v[len(v)-4:]
}
