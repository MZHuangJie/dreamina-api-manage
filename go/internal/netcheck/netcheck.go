// Package netcheck 检查一个账号当前的**出口 IP 归属地**是否和它的
// 平台归属地对得上。
//
// 为什么需要它：账号的归属地（store-idc / store-country-code）在注册时就定死了，
// 换 VPN 节点并不会改变它。但平台会看请求从哪来——出口国家和账号归属地
// 不一致时，轻则报「无权限」，重则判定异常并让 sessionid 失效。
//
// 所以「我换了个节点，账号还能用吗」这个问题，答案是可以用工具问出来的。
package netcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"dreamina-manager/internal/sysproxy"
)

// ExitInfo 是一次出口体检的结果。
type ExitInfo struct {
	// IP 是出口公网 IP。
	IP string `json:"ip"`
	// Country 是出口国家代码（小写，如 us / jp / vn）。
	Country string `json:"country"`
	// CountryName 是国家名，仅用于展示。
	CountryName string `json:"countryName"`
	// ISP 是运营商，可用于判断是不是机房 IP。
	ISP string `json:"isp"`
	// Source 是哪个服务给出的结果。
	Source string `json:"source"`
	// LatencyMS 是到该服务的一次往返耗时，顺带反映节点质量。
	LatencyMS int64 `json:"latencyMs"`
}

// Result 是体检结论。
type Result struct {
	Exit ExitInfo `json:"exit"`
	// ExpectedCountry 是账号在平台上的归属国家。
	ExpectedCountry string `json:"expectedCountry"`
	// Match 表示出口国家与账号归属地是否一致。
	Match bool `json:"match"`
	// Verdict 是一句人话结论。
	Verdict string `json:"verdict"`
	// Advice 是建议采取的动作（如有）。
	Advice string `json:"advice,omitempty"`
	// ProxyUsed 表示这次体检是否走了代理。
	ProxyUsed bool `json:"proxyUsed"`
}

// geoEndpoint 是一个 IP 归属地查询服务。
type geoEndpoint struct {
	name string
	url  string
	// parse 把响应解析成 ip / country / countryName / isp。
	parse func([]byte) (ip, country, countryName, isp string, err error)
}

// endpoints 按顺序尝试。写多个是因为单一服务随时可能挂掉或被墙，
// 而这类体检恰恰是在网络不稳的时候最需要。
var endpoints = []geoEndpoint{
	{
		// Cloudflare 放第一个：它几乎没有被墙或限流的可能，
		// 而且直接回纯文本，解析最快。实测在某些「只代理部分流量」的
		// VPN 规则下，它是唯一能通的。
		name: "cloudflare.com",
		url:  "https://www.cloudflare.com/cdn-cgi/trace",
		parse: func(raw []byte) (string, string, string, string, error) {
			var ip, loc string
			for _, line := range strings.Split(string(raw), string(byte(10))) {
				switch {
				case strings.HasPrefix(line, "ip="):
					ip = strings.TrimSpace(strings.TrimPrefix(line, "ip="))
				case strings.HasPrefix(line, "loc="):
					loc = strings.TrimSpace(strings.TrimPrefix(line, "loc="))
				}
			}
			if ip == "" {
				return "", "", "", "", fmt.Errorf("cloudflare trace 未返回 ip")
			}
			return ip, strings.ToLower(loc), "", "", nil
		},
	},
	{
		name: "ip-api.com",
		url:  "http://ip-api.com/json/?fields=status,country,countryCode,query,isp",
		parse: func(raw []byte) (string, string, string, string, error) {
			var v struct {
				Status      string `json:"status"`
				Message     string `json:"message"`
				Country     string `json:"country"`
				CountryCode string `json:"countryCode"`
				Query       string `json:"query"`
				ISP         string `json:"isp"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return "", "", "", "", err
			}
			if v.Status != "success" {
				return "", "", "", "", fmt.Errorf("ip-api 返回失败: %s", v.Message)
			}
			return v.Query, strings.ToLower(v.CountryCode), v.Country, v.ISP, nil
		},
	},
	{
		name: "ipapi.co",
		url:  "https://ipapi.co/json/",
		parse: func(raw []byte) (string, string, string, string, error) {
			var v struct {
				IP          string `json:"ip"`
				CountryCode string `json:"country_code"`
				CountryName string `json:"country_name"`
				Org         string `json:"org"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return "", "", "", "", err
			}
			if v.IP == "" {
				return "", "", "", "", fmt.Errorf("ipapi 未返回 IP")
			}
			return v.IP, strings.ToLower(v.CountryCode), v.CountryName, v.Org, nil
		},
	},
	{
		name: "ipinfo.io",
		url:  "https://ipinfo.io/json",
		parse: func(raw []byte) (string, string, string, string, error) {
			var v struct {
				IP      string `json:"ip"`
				Country string `json:"country"`
				Org     string `json:"org"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return "", "", "", "", err
			}
			if v.IP == "" {
				return "", "", "", "", fmt.Errorf("ipinfo 未返回 IP")
			}
			return v.IP, strings.ToLower(v.Country), "", v.Org, nil
		},
	},
}

// Check 通过指定代理查一次出口 IP 归属地。
//
// proxyURL 留空则走直连——这时的结果是「本机出口」，
// 用于判断当前 VPN 节点是哪个国家。
func Check(ctx context.Context, proxyURL string) (*ExitInfo, error) {
	client, err := buildClient(ResolveProxy(proxyURL))
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, ep := range endpoints {
		started := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("accept", "application/json")
		req.Header.Set("user-agent", "dreamina-manager/netcheck")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s 不可达: %w", ep.name, err)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		ip, country, countryName, isp, parseErr := ep.parse(raw)
		if parseErr != nil {
			lastErr = fmt.Errorf("%s: %w", ep.name, parseErr)
			continue
		}
		return &ExitInfo{
			IP:          ip,
			Country:     country,
			CountryName: countryName,
			ISP:         isp,
			Source:      ep.name,
			LatencyMS:   time.Since(started).Milliseconds(),
		}, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的 IP 归属地查询服务")
	}
	// 带上这一句，因为最常见的失败原因就是代理本身连不通
	return nil, fmt.Errorf("查不到出口 IP（%w）。如果配了代理，先确认代理可用", lastErr)
}

// Compare 把出口信息和账号归属地比对，给出结论。
func Compare(exit *ExitInfo, expectedCountry string, proxyUsed bool) *Result {
	expected := strings.ToLower(strings.TrimSpace(expectedCountry))
	result := &Result{
		Exit:            *exit,
		ExpectedCountry: expected,
		ProxyUsed:       proxyUsed,
		Match:           expected == "" || exit.Country == expected,
	}

	switch {
	case expected == "":
		result.Verdict = fmt.Sprintf("出口在 %s（%s），账号未记录归属地，无法比对",
			displayCountry(exit), exit.IP)
		result.Advice = "在账号详情里确认 store-country-code 是否抽到了"

	case exit.Country == expected:
		result.Verdict = fmt.Sprintf("出口在 %s，与账号归属地一致，正常",
			displayCountry(exit))

	case !proxyUsed:
		result.Verdict = fmt.Sprintf("出口在 %s，但账号归属地是 %s —— **不一致**",
			displayCountry(exit), strings.ToUpper(expected))
		result.Advice = "账号在直连状态下会暴露本机出口。给它配一个归属地的代理，" +
			"或者把 VPN 节点切到该国家"

	default:
		result.Verdict = fmt.Sprintf("代理出口在 %s，但账号归属地是 %s —— **不一致**",
			displayCountry(exit), strings.ToUpper(expected))
		result.Advice = "换一个归属地匹配的代理；用不匹配的出口访问，" +
			"轻则报无权限，重则让 sessionid 失效"
	}

	return result
}

func displayCountry(exit *ExitInfo) string {
	switch {
	case exit.CountryName != "" && exit.Country != "":
		return fmt.Sprintf("%s(%s)", exit.CountryName, strings.ToUpper(exit.Country))
	case exit.Country != "":
		return strings.ToUpper(exit.Country)
	default:
		return "未知地区"
	}
}

// ResolveProxy 决定体检时实际走哪个出口：账号代理 > 系统代理 > 直连。
//
// 必须和 Dreamina 客户端的逻辑保持一致，否则体检结果会骗人——
// 比如客户端走了系统代理，体检却按直连测，就会报一个假的「不一致」。
func ResolveProxy(accountProxy string) string {
	if trimmed := strings.TrimSpace(accountProxy); trimmed != "" {
		return trimmed
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MANAGER_IGNORE_SYSTEM_PROXY")), "true") {
		return ""
	}
	detected := sysproxy.Detect()
	if !sysproxy.IsUsable(detected.URL) {
		return ""
	}
	return detected.URL
}

func buildClient(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效 %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport}, nil
}
