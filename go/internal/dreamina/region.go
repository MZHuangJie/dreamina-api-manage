package dreamina

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"dreamina-manager/internal/apisign"
)

// AID 是 Dreamina 的应用 ID（即梦是 513695，两者不同）。
const AID = "513641"

// WebVersion / DaVersion 是 Dreamina 网页端的版本号。
const (
	WebVersion = "7.5.0"
	DaVersion  = "3.3.28"
)

// Endpoints 描述一个区域集群的接入点。
//
// 这是接入 Dreamina 最容易踩的坑：**API 主机是按账号归属地划分的**。
// 把美区账号打到新加坡集群会得到 1015 login error，
// 看起来像 sessionid 失效，其实只是打错了地方。
type Endpoints struct {
	// Name 是人类可读的集群名（US / SG / JP …），用于日志与展示。
	Name string
	// Region 是 URL 上的 region 参数，必须与集群一致。
	Region string
	// API 承载 /mweb/v1/* 业务接口。
	API string
	// Commerce 承载积分、订阅等接口。
	Commerce string
}

// 已知的集群。
//
// US 和 SG 是实测确认过的。其余是按命名规律推断的候选——
// 它们的价值在于「可以被自动探测验证」，而不是「一定正确」。
// 系统遇到 1015 会依次试候选并把成功的那个记住，所以列进来无害。
var knownClusters = []Endpoints{
	{Name: "US", Region: "US", API: "dreamina-api.us.capcut.com", Commerce: "commerce-us-ttp2.us.capcut.com"},
	{Name: "US-TTP2", Region: "US", API: "dreamina-api-us-ttp2.us.capcut.com", Commerce: "commerce-us-ttp2.us.capcut.com"},
	{Name: "SG", Region: "SG", API: "mweb-api-sg.capcut.com", Commerce: "commerce-api-sg.capcut.com"},
	{Name: "SG-TTP2", Region: "SG", API: "mweb-api-sg-ttp2.capcut.com", Commerce: "commerce-api-sg.capcut.com"},
	{Name: "JP", Region: "JP", API: "mweb-api-jp.capcut.com", Commerce: "commerce-api-jp.capcut.com"},
	{Name: "JP-TTP2", Region: "JP", API: "mweb-api-jp-ttp2.capcut.com", Commerce: "commerce-api-jp.capcut.com"},
	{Name: "EU", Region: "EU", API: "mweb-api-eu.capcut.com", Commerce: "commerce-api-eu.capcut.com"},
}

// 账号国家 → 优先尝试的集群名。顺序即优先级。
var countryPreference = map[string][]string{
	"us": {"US", "US-TTP2", "SG"},
	"ca": {"US", "US-TTP2", "SG"},
	"mx": {"US", "US-TTP2"},
	"br": {"US", "US-TTP2"},

	"sg": {"SG", "SG-TTP2"},
	"my": {"SG", "SG-TTP2"},
	"id": {"SG", "SG-TTP2"},
	"th": {"SG", "SG-TTP2"},
	"ph": {"SG", "SG-TTP2"},
	"vn": {"SG", "SG-TTP2"},

	"jp": {"JP", "JP-TTP2", "SG"},
	"kr": {"JP", "JP-TTP2", "SG"},
	"tw": {"JP", "JP-TTP2", "SG"},
	"hk": {"JP", "JP-TTP2", "SG"},

	"gb": {"EU", "US"},
	"de": {"EU", "US"},
	"fr": {"EU", "US"},
}

// 账号国家 → 该地区的代表时区。
//
// 时区不对是一个不必要的指纹差异：账号在日区、代理在日本，
// 浏览器却报美国时间，很容易被风控注意到。
var countryTimezone = map[string]string{
	"us": "America/Los_Angeles",
	"ca": "America/Toronto",
	"mx": "America/Mexico_City",
	"br": "America/Sao_Paulo",

	"sg": "Asia/Singapore",
	"my": "Asia/Kuala_Lumpur",
	"id": "Asia/Jakarta",
	"th": "Asia/Bangkok",
	"ph": "Asia/Manila",
	"vn": "Asia/Ho_Chi_Minh",

	"jp": "Asia/Tokyo",
	"kr": "Asia/Seoul",
	"tw": "Asia/Taipei",
	"hk": "Asia/Hong_Kong",

	"gb": "Europe/London",
	"de": "Europe/Berlin",
	"fr": "Europe/Paris",
}

// extraClusters 是用户通过配置文件补充的集群，优先级最高。
var (
	extraMu       sync.RWMutex
	extraClusters []Endpoints
)

// LoadExtraClusters 从 JSON 文件加载额外的集群定义。
//
// 文件格式（数组，字段与 Endpoints 对应）：
//
//	[
//	  {"name":"JP","region":"JP","api":"mweb-api-jp.capcut.com","commerce":"commerce-api-jp.capcut.com"}
//	]
//
// 存在的意义：新区域出现时不必改代码重新编译，改个配置就能接上。
func LoadExtraClusters(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 没有配置文件是正常情况
		}
		return err
	}
	var parsed []Endpoints
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	extraMu.Lock()
	extraClusters = parsed
	extraMu.Unlock()
	return nil
}

func allClusters() []Endpoints {
	extraMu.RLock()
	defer extraMu.RUnlock()
	out := make([]Endpoints, 0, len(extraClusters)+len(knownClusters))
	// 用户配置的排前面，优先尝试
	out = append(out, extraClusters...)
	out = append(out, knownClusters...)
	return out
}

func findCluster(name string) (Endpoints, bool) {
	for _, c := range allClusters() {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Endpoints{}, false
}

// idcClusterHints 把 store-idc 前缀映射到集群名。
//
// store-idc 形如 useast5 / useast8 / sg1 / my2 / jp1。
var idcClusterHints = []struct {
	Prefix  string
	Cluster string
}{
	{"useast", "US"}, {"uswest", "US"}, {"us-", "US"}, {"va", "US"},
	{"sg", "SG"}, {"my", "SG"}, {"sea", "SG"}, {"id", "SG"},
	{"jp", "JP"}, {"apne", "JP"}, {"ap-northeast", "JP"},
	{"eu", "EU"}, {"ie", "EU"}, {"de", "EU"}, {"gb", "EU"}, {"london", "EU"},
}

// clusterCache 记住某个 (store-idc, country) 实际探测成功的集群。
//
// 探测一次就够了，之后的请求直接用结果。键是账号的归属地信息，
// 同一地区的多个账号共享缓存。
var clusterCache sync.Map

func cacheKey(idc, country string) string {
	return strings.ToLower(strings.TrimSpace(idc)) + "|" + strings.ToLower(strings.TrimSpace(country))
}

// RememberCluster 记下某归属地实际可用的集群。
func RememberCluster(storeIDC, storeCountry string, endpoints Endpoints) {
	clusterCache.Store(cacheKey(storeIDC, storeCountry), endpoints)
}

// ForgetCluster 清掉缓存，下次重新探测。
func ForgetCluster(storeIDC, storeCountry string) {
	clusterCache.Delete(cacheKey(storeIDC, storeCountry))
}

// CachedCluster 返回缓存的集群（如果有）。
func CachedCluster(storeIDC, storeCountry string) (Endpoints, bool) {
	if v, ok := clusterCache.Load(cacheKey(storeIDC, storeCountry)); ok {
		if e, ok := v.(Endpoints); ok {
			return e, true
		}
	}
	return Endpoints{}, false
}

// Candidates 返回该账号应该依次尝试的集群。
//
// 第一个是「最可能正确」的：缓存结果 > store-idc 推断 > 国家推断 > 美区兜底。
// 后面的候选用于在遇到 1015 时自动换一个再试——
// 这样即使新区域的主机名我们没猜中，系统也能自己找到能用的那个。
func Candidates(storeIDC, storeCountryCode string) []Endpoints {
	if cached, ok := CachedCluster(storeIDC, storeCountryCode); ok {
		return append([]Endpoints{cached}, excludeCluster(cached.Name)...)
	}

	ordered := []Endpoints{}
	add := func(name string) {
		if e, ok := findCluster(name); ok {
			for _, existing := range ordered {
				if strings.EqualFold(existing.Name, e.Name) {
					return
				}
			}
			ordered = append(ordered, e)
		}
	}

	idc := strings.ToLower(strings.TrimSpace(storeIDC))
	country := strings.ToLower(strings.TrimSpace(storeCountryCode))

	for _, hint := range idcClusterHints {
		if strings.HasPrefix(idc, hint.Prefix) {
			add(hint.Cluster)
			break
		}
	}
	for _, name := range countryPreference[country] {
		add(name)
	}
	// 兜底：其余集群按原顺序排在后面，用于自动探测
	for _, c := range allClusters() {
		add(c.Name)
	}

	if len(ordered) == 0 {
		ordered = append(ordered, allClusters()...)
	}
	return ordered
}

func excludeCluster(name string) []Endpoints {
	out := []Endpoints{}
	for _, c := range allClusters() {
		if !strings.EqualFold(c.Name, name) {
			out = append(out, c)
		}
	}
	return out
}

// ResolveEndpoints 返回首选集群。保留它是为了兼容只想要一个结果的调用方。
func ResolveEndpoints(storeIDC, storeCountryCode string) (Endpoints, error) {
	candidates := Candidates(storeIDC, storeCountryCode)
	if len(candidates) == 0 {
		return Endpoints{}, fmt.Errorf("没有任何可用的集群配置")
	}
	return candidates[0], nil
}

// TimezoneFor 返回该国家对应的代表时区；未知时返回空串。
func TimezoneFor(storeCountryCode string) string {
	return countryTimezone[strings.ToLower(strings.TrimSpace(storeCountryCode))]
}

// CountryCodes 返回所有已知的国家代码，供界面下拉使用。
func CountryCodes() []string {
	out := make([]string, 0, len(countryPreference))
	for code := range countryPreference {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// APIURL 拼出带公共 query 的完整 URL。
func (e Endpoints) APIURL(pathname string, extra ...string) string {
	return e.url("https://"+e.API, pathname, extra...)
}

// CommerceURL 拼出电商接口的完整 URL。
func (e Endpoints) CommerceURL(pathname string, extra ...string) string {
	return e.url("https://"+e.Commerce, pathname, extra...)
}

func (e Endpoints) url(host, pathname string, extra ...string) string {
	parts := []string{
		"aid=" + AID,
		"device_platform=web",
		"region=" + e.Region,
		"da_version=" + DaVersion,
		"os=windows",
		"web_version=" + WebVersion,
		"aigc_features=app_lip_sync",
	}
	parts = append(parts, extra...)
	return host + pathname + "?" + strings.Join(parts, "&")
}

// HeadersOptions 是构造请求头时需要的外部信息。
type HeadersOptions struct {
	Pathname string
	// Country 是**账号的**归属国家（如 vn / jp），不是集群的 region。
	//
	// 这两个必须分开：一个越南账号走新加坡集群时，
	// region 参数是 SG，但 loc / store-country-code 仍然是 vn。
	// 混用会让平台看到自相矛盾的归属信号。
	Country string
}

// CommonHeaders 返回所有 mweb 接口都要带的请求头。
func (e Endpoints) CommonHeaders(opts HeadersOptions) map[string]string {
	sign, deviceTime := SignNow(opts.Pathname)

	country := strings.ToLower(strings.TrimSpace(opts.Country))
	if country == "" {
		// 没给国家信息时退回集群 region，至少不会更差
		country = strings.ToLower(e.Region)
	}

	return map[string]string{
		"accept":                 "application/json, text/plain, */*",
		"accept-language":        "en-US,en;q=0.9",
		"app-sdk-version":        "48.0.0",
		"appid":                  AID,
		"appvr":                  AppVersion,
		"pf":                     apisign.PlatformCode,
		"sign-ver":               "1",
		"sign":                   sign,
		"device-time":            fmt.Sprintf("%d", deviceTime),
		"lan":                    "en",
		"loc":                    country,
		"store-country-code":     country,
		"store-country-code-src": "uid",
		"tdid":                   "",
		"content-type":           "application/json",
	}
}
