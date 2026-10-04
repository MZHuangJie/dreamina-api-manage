// Package apisign 提供即梦系接口的请求签名。
//
// 即梦（国内版）与 Dreamina（海外版）用的是**同一个公式**，
// 只有 appvr 不同：
//
//	md5("9e2c|" + path 后 7 位 + "|" + pf + "|" + appvr + "|" + 秒级时间戳 + "||11ac")
//
// 末尾那段是空串（tdid 位）。这个公式被两次真实抓包逐字节验证过：
//
//	md5("9e2c|enerate|7|8.4.0|1790786145||11ac") = 2533662377211f0653b7759901657a92
//	md5("9e2c|enerate|7|8.4.0|1790790023||11ac") = 944f6daeea95d3dcec817a90aa858647
package apisign

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"time"
)

// PlatformCode 是固定的平台标识（请求头 pf）。
const PlatformCode = "7"

// Sign 计算签名。
func Sign(pathname, appvr string, unixSeconds int64) string {
	tail := pathname
	if len(tail) > 7 {
		tail = tail[len(tail)-7:]
	}
	raw := "9e2c|" + tail + "|" + PlatformCode + "|" + appvr + "|" +
		strconv.FormatInt(unixSeconds, 10) + "||11ac"
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// SignNow 用当前时间签名，并返回配套的 device-time。
func SignNow(pathname, appvr string) (sign string, deviceTime int64) {
	deviceTime = time.Now().Unix()
	return Sign(pathname, appvr, deviceTime), deviceTime
}
