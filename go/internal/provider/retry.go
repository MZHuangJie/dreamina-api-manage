package provider

import (
	"errors"
	"strings"
)

// ErrNoWorkspace 表示账号还没在网页端使用过，因而拿不到提交所需的 workspace。
//
// 这是账号自身状态问题——换个号就能继续，所以算「换号可能有用」。
var ErrNoWorkspace = errors.New("该账号没有任何 workspace，请先在网页端使用一次")

// ErrContentRejected 表示平台以内容违规为由拒绝了这次生成。
//
// 关键区别：**换号没有用**。同一个 prompt 换哪个账号都会被同样审核掉，
// 盲目重试只会白白消耗其它账号的额度并留下无用记录。
var ErrContentRejected = errors.New("内容未通过平台审核")

// AccountFault 判断一个错误是不是「这个账号的问题」。
//
// 返回 true 表示换一个账号重试有实际意义；返回 false 表示重试是浪费，
// 应当直接把失败暴露给调用方。
func AccountFault(err error) bool {
	if err == nil {
		return false
	}

	// 审核判定必须放在最前面。
	//
	// 平台对违规内容是「先通过鉴权、再在业务层拒绝」，所以这类失败
	// 往往同时挂着 AuthError 之类的壳。如果先判账号问题，就会把一个
	// 注定失败的 prompt 拿去把所有账号撞一遍——白烧额度，还在平台侧
	// 留下一串违规提交记录。
	if errors.Is(err, ErrContentRejected) || IsContentRejected(err) {
		return false
	}

	// 账号自身的状态问题
	if IsAuthError(err) {
		return true
	}
	if AsInsufficientCredit(err) {
		return true
	}
	if errors.Is(err, ErrNoWorkspace) {
		return true
	}

	// 剩下的按网络/传输类问题处理：代理挂了、连接被重置、超时等，
	// 换个出口大概率能过。
	return isTransportFault(err)
}

// IsContentRejected 兜底识别审核类失败。
//
// 平台没有给稳定的错误码，只能按文案判断——所以这里刻意写得保守，
// 只在明确命中审核语义时才认定为不可重试。
func IsContentRejected(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, marker := range []string{"审核", "违规", "content policy", "moderation", "sensitive"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// isTransportFault 识别网络/代理层的故障。
func isTransportFault(err error) bool {
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"proxy", "代理",
		"timeout", "超时", "deadline exceeded",
		"connection reset", "connection refused", "eof",
		"no such host", "dial tcp", "tls handshake",
		"shark not pass", // 风控拦截：换个账号/出口常常能过
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
