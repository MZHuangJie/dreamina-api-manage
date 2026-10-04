export type HealthStatus = "unknown" | "healthy" | "expired" | "error";
export type ProviderName = "jimeng" | "dreamina";

/** 聚合网关的接入密钥。明文只在创建时返回一次。 */
export interface APIKey {
  id: string;
  name: string;
  remark: string;
  keyPrefix: string;
  enabled: boolean;
  usageCount: number;
  lastUsedAt: string;
  createdAt: string;
  /** 以下配额 0 一律表示不限 */
  ratePerMinute: number;
  maxConcurrent: number;
  dailyQuota: number;
  /** 今日已用配额 */
  todayUsed: number;
}
export type CredentialKind = "sessionid" | "cookie";
export type SelectionStrategy = "active" | "least_failures" | "round_robin" | "most_credit";

export interface MembershipInfo {
  isVip: boolean | null;
  vipExpireAt: string | null;
  membershipType: string | null;
}

export interface Account {
  id: string;
  name: string;
  remark: string;
  tags: string[];
  provider: ProviderName;
  credentialKind: CredentialKind;
  credentialFingerprint: string;
  storeIdc: string;
  storeCountry: string;
  enabled: boolean;
  isActive: boolean;
  proxyEnabled: boolean;
  proxyUrlMasked: string;
  health: HealthStatus;
  userId: string | null;
  nickname: string | null;
  avatarUrl: string | null;
  freeCredit: number | null;
  purchaseCredit: number | null;
  vipCredit: number | null;
  totalCredit: number | null;
  membership: MembershipInfo;
  statusCheckedAt: string | null;
  statusError: string | null;
  successCount: number;
  failureCount: number;
  cooldownUntil: string | null;
  inCooldown: boolean;
  lastUsedAt: string | null;
  lastSuccessAt: string | null;
  lastFailureAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface ProbeRunState {
  running: boolean;
  startedAt: string | null;
  finishedAt: string | null;
  total: number;
  completed: number;
  healthy: number;
  failed: number;
  lastError: string | null;
}

export interface Overview {
  total: number;
  enabled: number;
  healthy: number;
  expired: number;
  errorCount: number;
  unknown: number;
  inCooldown: number;
  withProxy: number;
  totalCredit: number;
  vipCount: number;
  activeAccountId: string | null;
  probe: ProbeRunState;
  strategy: SelectionStrategy;
  activeAccount: Account | null;
}

export interface AccountEvent {
  id: number;
  level: "info" | "warn" | "error" | string;
  kind: string;
  message: string;
  detail: string | null;
  created_at: string;
}

export interface ImageModel {
  id: string;
  reqKey: string;
  label: string;
  resolutions: string[];
  defaultResolution: string;
  creditPerImage: number;
}

export interface VideoModel {
  id: string;
  reqKey: string;
  label: string;
  resolutions: string[];
  defaultResolution: string;
  durations: number[];
  defaultDuration: number;
  creditPerSecond: number;
}

export interface ModelCatalog {
  image: ImageModel[];
  video: VideoModel[];
  imageRatios: string[];
  videoRatios: string[];
}

export type GenerationMode = "text2image" | "image2image" | "text2video" | "image2video";
export type GenerationStatus = "pending" | "running" | "succeeded" | "failed" | "canceled";

export interface GenerationRecord {
  id: string;
  accountId: string | null;
  accountName: string | null;
  kind: "image" | "video";
  mode: GenerationMode;
  model: string;
  modelLabel: string;
  prompt: string;
  params: Record<string, unknown>;
  status: GenerationStatus;
  progress: string | null;
  historyId: string | null;
  result: { urls?: string[]; url?: string } | null;
  error: string | null;
  creditEstimate: number;
  createdAt: string;
  updatedAt: string;
}

export interface GenerationStats {
  total: number;
  running: number;
  succeeded: number;
  failed: number;
  images: number;
  videos: number;
}

export interface BulkImportResult {
  created: number;
  failed: number;
  accounts: Account[];
  errors: { line: number; value: string; reason: string }[];
}
