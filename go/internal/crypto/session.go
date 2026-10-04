package crypto

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	bareCredential = regexp.MustCompile("^[^\\s;]+$")
	cookiePrefix   = regexp.MustCompile("(?i)^\\s*cookie\\s*:\\s*")
	lineBreak      = regexp.MustCompile("[\\r\\n]+")
)

// ExtractSessionID 从用户粘贴的内容里解析出 sessionid。
//
// 兼容浏览器开发者工具复制出来的多种形态：
//
//	纯 sessionid
//	sessionid=xxx
//	Cookie: a=b; c=d（多行粘贴也认）
//
// 依次尝试 sessionid / sessionid_ss / sid_tt / sid_guard。
func ExtractSessionID(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", errors.New("凭据不能为空")
	}

	normalized := lineBreak.ReplaceAllString(cookiePrefix.ReplaceAllString(text, ""), ";")

	pairs := map[string]string{}
	for _, item := range strings.Split(normalized, ";") {
		idx := strings.Index(item, "=")
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item[:idx]))
		value := strings.TrimSpace(item[idx+1:])
		if key != "" && value != "" {
			pairs[key] = value
		}
	}

	value := pairs["sessionid"]
	if value == "" {
		value = pairs["sessionid_ss"]
	}
	if value == "" {
		value = pairs["sid_tt"]
	}
	if value == "" {
		if guard := pairs["sid_guard"]; guard != "" {
			if part, _, found := strings.Cut(guard, "%7C"); found {
				value = part
			} else if part, _, found := strings.Cut(guard, "|"); found {
				value = part
			}
		}
	}
	// 允许直接粘贴裸 sessionid
	if value == "" && !strings.Contains(normalized, "=") && bareCredential.MatchString(text) {
		value = text
	}
	if value == "" {
		return "", errors.New("未能从凭据中识别 sessionid，请确认复制的是登录后的 Cookie 或 sessionid")
	}
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded, nil
	}
	return value, nil
}

// ExtractCookieValue 取出指定名字的 cookie 值（用于读 store-idc / store-country-code）。
func ExtractCookieValue(raw, name string) string {
	normalized := lineBreak.ReplaceAllString(raw, ";")
	want := strings.ToLower(name)
	for _, item := range strings.Split(normalized, ";") {
		idx := strings.Index(item, "=")
		if idx < 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item[:idx])) == want {
			return strings.TrimSpace(item[idx+1:])
		}
	}
	return ""
}
