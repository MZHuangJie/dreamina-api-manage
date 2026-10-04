/** 即梦接口返回体统一信封 */
export interface JimengEnvelope<T = unknown> {
  ret?: string | number;
  errmsg?: string;
  data?: T;
  [key: string]: unknown;
}

export class JimengError extends Error {
  readonly code: string | null;
  readonly status: number | null;
  readonly retryable: boolean;

  constructor(
    message: string,
    options: { code?: string | null; status?: number | null; retryable?: boolean } = {}
  ) {
    super(message);
    this.name = "JimengError";
    this.code = options.code ?? null;
    this.status = options.status ?? null;
    this.retryable = options.retryable ?? false;
  }
}

/** 登录态失效 / 未登录 —— 账号需要重新登录 */
export class JimengAuthError extends JimengError {
  constructor(message = "登录态已失效，请重新登录") {
    super(message, { code: "AUTH_EXPIRED" });
    this.name = "JimengAuthError";
  }
}

/** 积分不足 */
export class JimengCreditError extends JimengError {
  constructor(message = "积分不足") {
    super(message, { code: "INSUFFICIENT_CREDIT" });
    this.name = "JimengCreditError";
  }
}

/** 网络 / 代理层失败 —— 通常是可重试的 */
export class JimengNetworkError extends JimengError {
  constructor(message: string) {
    super(message, { code: "NETWORK", retryable: true });
    this.name = "JimengNetworkError";
  }
}

/** 代理不可用 —— 单独区分，便于前端提示 */
export class JimengProxyError extends JimengError {
  constructor(message: string) {
    super(message, { code: "PROXY" });
    this.name = "JimengProxyError";
  }
}

/** 即梦返回的 ret 码 */
const AUTH_RET_CODES = new Set(["8", "1000", "1001", "1002", "1015", "100010"]);
const CREDIT_RET_CODES = new Set(["5000", "1006"]);
const AUTH_MESSAGE_PATTERN = /(未登录|请先登录|登录已过期|登录态|重新登录|not\s*log(ged)?\s*in|login\s*required|session\s*(expired|invalid))/i;

export function classifyRet(ret: string, errmsg: string): JimengError | null {
  if (ret === "0") return null;
  if (CREDIT_RET_CODES.has(ret)) return new JimengCreditError(`积分不足：${errmsg}（错误码 ${ret}）`);
  if (AUTH_RET_CODES.has(ret) || AUTH_MESSAGE_PATTERN.test(errmsg)) {
    return new JimengAuthError(`登录态失效：${errmsg}（错误码 ${ret}）`);
  }
  return new JimengError(`即梦接口错误：${errmsg}（错误码 ${ret}）`, { code: ret });
}
