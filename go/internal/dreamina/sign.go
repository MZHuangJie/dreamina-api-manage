package dreamina

import (
	"dreamina-manager/internal/apisign"
)

// AppVersion 是 Dreamina 网页端的 appvr，签名会校验它。
//
// 与即梦不同：即梦的 appvr 跟着 web_version 走（5.8.0），
// Dreamina 用它自己的 8.4.0，两者不可混用。
const AppVersion = "8.4.0"

// Sign 计算 Dreamina 的请求签名。公式与即梦同源，见 apisign 包注释。
func Sign(pathname string, unixSeconds int64) string {
	return apisign.Sign(pathname, AppVersion, unixSeconds)
}

// SignNow 用当前时间计算签名，并返回配套的 device-time。
func SignNow(pathname string) (sign string, deviceTime int64) {
	return apisign.SignNow(pathname, AppVersion)
}
