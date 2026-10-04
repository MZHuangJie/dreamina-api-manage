import { jimengRequest } from "./client.ts";

/** 每账号一次探活的完整结果 */
export interface AccountProbeResult {
  userId: string | null;
  nickname: string | null;
  avatarUrl: string | null;
  freeCredit: number | null;
  purchaseCredit: number | null;
  vipCredit: number | null;
  totalCredit: number | null;
  membership: MembershipInfo;
}

export interface MembershipInfo {
  isVip: boolean | null;
  vipExpireAt: string | null;
  membershipType: string | null;
  raw?: unknown;
}

export interface ProbeCredential {
  accountId: string;
  credential: string;
  credentialKind: "sessionid" | "cookie";
  proxyUrl?: string | null;
}

function pickString(source: Record<string, unknown> | null | undefined, keys: string[]): string | null {
  if (!source) return null;
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "string" && value.trim()) return value;
    if (typeof value === "number") return String(value);
  }
  return null;
}

function pickNumber(source: Record<string, unknown> | null | undefined, keys: string[]): number | null {
  if (!source) return null;
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() && Number.isFinite(Number(value))) return Number(value);
  }
  return null;
}

/** 账号信息 / 登录态。这也是判断 sessionid 是否仍然有效的权威接口。 */
export async function fetchAccountInfo(credential: ProbeCredential): Promise<Record<string, unknown>> {
  const data = await jimengRequest<Record<string, unknown>>({
    accountId: credential.accountId,
    uri: "/passport/account/info/v2",
    credential: credential.credential,
    credentialKind: credential.credentialKind,
    params: { account_sdk_source: "web" },
    data: {},
    proxyUrl: credential.proxyUrl,
    referer: "https://jimeng.jianying.com/ai-tool/home"
  });
  return data ?? {};
}

/** 积分余额 */
export async function fetchCredit(credential: ProbeCredential): Promise<{
  freeCredit: number | null;
  purchaseCredit: number | null;
  vipCredit: number | null;
  totalCredit: number | null;
}> {
  const data = await jimengRequest<{ credit?: Record<string, unknown> }>({
    accountId: credential.accountId,
    uri: "/commerce/v1/benefits/user_credit",
    credential: credential.credential,
    credentialKind: credential.credentialKind,
    data: {},
    proxyUrl: credential.proxyUrl
  });

  const credit = (data?.credit ?? {}) as Record<string, unknown>;
  const free = pickNumber(credit, ["gift_credit", "free_credit"]);
  const purchase = pickNumber(credit, ["purchase_credit"]);
  const vip = pickNumber(credit, ["vip_credit"]);
  const total =
    pickNumber(credit, ["total_credit", "cur_total_credits"]) ??
    (free ?? 0) + (purchase ?? 0) + (vip ?? 0);

  return { freeCredit: free, purchaseCredit: purchase, vipCredit: vip, totalCredit: total };
}

/** 领取每日赠送积分，返回领取后的总额 */
export async function receiveDailyCredit(credential: ProbeCredential): Promise<{
  received: number | null;
  totalCredit: number | null;
}> {
  const data = await jimengRequest<Record<string, unknown>>({
    accountId: credential.accountId,
    uri: "/commerce/v1/benefits/credit_receive",
    credential: credential.credential,
    credentialKind: credential.credentialKind,
    data: { time_zone: "Asia/Shanghai" },
    proxyUrl: credential.proxyUrl
  });
  return {
    received: pickNumber(data, ["receive_quota"]),
    totalCredit: pickNumber(data, ["cur_total_credits"])
  };
}

function extractMembership(info: Record<string, unknown>): MembershipInfo {
  const source = (info.user_info ?? info.user ?? info.account ?? info) as Record<string, unknown>;
  const vipFlag = source.is_vip ?? source.isVip ?? source.vip_status ?? null;
  const expire = pickString(source, ["vip_expire_time", "vipExpireTime", "vip_expire_at"]);
  const type = pickString(source, ["membership_type", "membershipType"]);
  const isVip =
    typeof vipFlag === "boolean" ? vipFlag : typeof vipFlag === "number" ? vipFlag > 0 : null;

  if (isVip === null && expire === null && type === null) {
    return { isVip: null, vipExpireAt: null, membershipType: null };
  }
  return { isVip, vipExpireAt: expire, membershipType: type };
}

/**
 * 一次探活：登录态 + 积分 + 会员。
 * 任一步骤抛错都会向上冒泡，由调用方决定标记为 expired 还是 error。
 */
export async function probeAccount(credential: ProbeCredential): Promise<AccountProbeResult> {
  const info = await fetchAccountInfo(credential);
  const userId = pickString(info, ["user_id", "userId", "uid"]);
  const nickname = pickString(info, ["name", "nickname", "user_name", "screen_name"]);
  const avatarUrl = pickString(info, ["avatar_url", "avatar_uri", "avatarUrl"]);

  let credit: Awaited<ReturnType<typeof fetchCredit>> = {
    freeCredit: null,
    purchaseCredit: null,
    vipCredit: null,
    totalCredit: null
  };
  try {
    credit = await fetchCredit(credential);
  } catch {
    // 积分接口偶发失败不应让整次探活失败
  }

  return {
    userId,
    nickname,
    avatarUrl,
    ...credit,
    membership: extractMembership(info)
  };
}
