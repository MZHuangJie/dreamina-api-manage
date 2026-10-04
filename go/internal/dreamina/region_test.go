package dreamina

import (
	"strings"
	"testing"
)

// 归属地要能推断出合理的首选集群。
//
// 这个测试覆盖了「不只是美区」这件事——之前只有 US/SG 两个映射，
// 日区账号会退回美区，表现为 1015 login error（看起来像登录失效，
// 其实只是打错了区域）。
func TestCandidatesByLocation(t *testing.T) {
	cases := []struct {
		name        string
		idc         string
		country     string
		wantFirst   string // 首选集群名
		wantContain string // 候选里必须出现的集群
	}{
		{"美区（按 idc）", "useast5", "us", "US", "US"},
		{"美区（按国家）", "", "us", "US", "US"},
		{"新加坡", "sg1", "sg", "SG", "SG"},
		{"越南走新加坡", "", "vn", "SG", "SG"},
		{"印尼", "id1", "id", "SG", "SG"},
		{"日区（按 idc）", "jp1", "jp", "JP", "JP"},
		{"日区（按国家）", "", "jp", "JP", "JP"},
		{"韩区", "", "kr", "JP", "JP"},
		{"台湾", "", "tw", "JP", "JP"},
		{"英国", "", "gb", "EU", "EU"},
		{"完全未知", "zz9", "zz", "US", "US"}, // 兜底到第一个候选
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ForgetCluster(tc.idc, tc.country)
			candidates := Candidates(tc.idc, tc.country)
			if len(candidates) == 0 {
				t.Fatal("不应返回空候选列表")
			}
			if candidates[0].Name != tc.wantFirst {
				t.Errorf("首选集群 = %s，期望 %s（候选顺序: %s）",
					candidates[0].Name, tc.wantFirst, clusterNames(candidates))
			}
			if !containsCluster(candidates, tc.wantContain) {
				t.Errorf("候选里应当包含 %s，实际: %s", tc.wantContain, clusterNames(candidates))
			}
			// 首位之后要还有别的候选，否则探测机制形同虚设
			if len(candidates) < 2 {
				t.Errorf("候选应当多于一个（用于自动探测），实际 %d 个", len(candidates))
			}
		})
	}
}

// 探测成功后要记住结果，后续请求直接用。
func TestRememberCluster(t *testing.T) {
	const idc, country = "jp1", "jp"
	ForgetCluster(idc, country)
	defer ForgetCluster(idc, country)

	first := Candidates(idc, country)[0]
	if first.Name == "US-TTP2" {
		t.Skip("首选恰好是待测集群，无法验证缓存生效")
	}

	target, ok := findCluster("US-TTP2")
	if !ok {
		t.Fatal("找不到 US-TTP2")
	}
	RememberCluster(idc, country, target)

	got := Candidates(idc, country)
	if got[0].Name != "US-TTP2" {
		t.Fatalf("缓存后首选应当是 US-TTP2，实际 %s", got[0].Name)
	}
	// 原来的首选仍应在候选中，只是排到后面
	found := false
	for _, c := range got[1:] {
		if c.Name == first.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("原首选 %s 应当保留在候选里", first.Name)
	}
}

// 请求头里的国家必须是**账号的**国家，不是集群的 region。
//
// 越南账号走 SG 集群时，region 参数是 SG，但 loc / store-country-code
// 必须是 vn——混用会让平台看到自相矛盾的归属信号。
func TestHeadersUseAccountCountryNotClusterRegion(t *testing.T) {
	var sg Endpoints
	for _, c := range allClusters() {
		if c.Name == "SG" {
			sg = c
		}
	}
	if sg.API == "" {
		t.Fatal("找不到 SG 集群")
	}

	headers := sg.CommonHeaders(HeadersOptions{
		Pathname: "/mweb/v1/aigc_draft/generate",
		Country:  "vn",
	})

	if headers["loc"] != "vn" {
		t.Errorf("loc = %q，期望 vn（账号国家，而不是集群 region）", headers["loc"])
	}
	if headers["store-country-code"] != "vn" {
		t.Errorf("store-country-code = %q，期望 vn", headers["store-country-code"])
	}
	if !strings.Contains(sg.APIURL("/x"), "region=SG") {
		t.Error("URL 里的 region 仍应当是集群的 SG")
	}
}

// 没给国家信息时退回集群 region，至少不会更差。
func TestHeadersFallBackToClusterRegion(t *testing.T) {
	var us Endpoints
	for _, c := range allClusters() {
		if c.Name == "US" {
			us = c
		}
	}
	headers := us.CommonHeaders(HeadersOptions{Pathname: "/x"})
	if headers["store-country-code"] != "us" {
		t.Errorf("缺省时应当退回集群 region，实际 %q", headers["store-country-code"])
	}
	// 签名头必须一直存在
	if headers["sign"] == "" || headers["device-time"] == "" {
		t.Error("签名头不应缺失")
	}
}

func TestTimezoneForCountry(t *testing.T) {
	cases := map[string]string{
		"jp": "Asia/Tokyo",
		"vn": "Asia/Ho_Chi_Minh",
		"us": "America/Los_Angeles",
		"sg": "Asia/Singapore",
	}
	for country, want := range cases {
		if got := TimezoneFor(country); got != want {
			t.Errorf("TimezoneFor(%s) = %s，期望 %s", country, got, want)
		}
	}
	// 未知国家返回空串，调用方会用默认值
	if got := TimezoneFor("zz"); got != "" {
		t.Errorf("未知国家应当返回空串，实际 %q", got)
	}
}

// 用户配置的额外集群要能覆盖内置定义。
func TestExtraClustersTakePriority(t *testing.T) {
	extraMu.Lock()
	saved := extraClusters
	extraClusters = []Endpoints{{Name: "ZZ", Region: "ZZ", API: "custom.example.com", Commerce: "c.example.com"}}
	extraMu.Unlock()
	defer func() {
		extraMu.Lock()
		extraClusters = saved
		extraMu.Unlock()
	}()

	e, ok := findCluster("ZZ")
	if !ok {
		t.Fatal("应当能按名字找到自定义集群")
	}
	if e.API != "custom.example.com" {
		t.Errorf("自定义集群的 API 主机不对: %s", e.API)
	}

	// 未知归属地的候选里应当包含自定义集群
	candidates := Candidates("unknown", "zz")
	if !containsCluster(candidates, "ZZ") {
		t.Errorf("候选里应当包含自定义集群，实际: %s", clusterNames(candidates))
	}
}

func containsCluster(list []Endpoints, name string) bool {
	for _, e := range list {
		if strings.EqualFold(e.Name, name) {
			return true
		}
	}
	return false
}

func clusterNames(list []Endpoints) string {
	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name)
	}
	return strings.Join(names, ", ")
}
