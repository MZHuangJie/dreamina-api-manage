import type { Account, AccountEvent } from "../types.ts";
import { HealthBadge, relTime } from "./ui.tsx";

const EVENT_TONE: Record<string, string> = {
  info: "var(--accent)",
  warn: "var(--amber)",
  error: "var(--red)"
};

function CreditPill({ label, value }: { label: string; value: number | null }) {
  return (
    <div className="credit-pill">
      <div className="n">{value ?? "—"}</div>
      <div className="l">{label}</div>
    </div>
  );
}

function KV({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="kv-item">
      <div className="kv-label">{label}</div>
      <div className={`kv-value${mono ? " mono" : ""}`}>{value}</div>
    </div>
  );
}

export function AccountDetail({
  account,
  events,
  busy,
  onActivate,
  onRelogin,
  onProbe,
  onReceiveCredit,
  onToggleEnabled,
  onResetHealth,
  onEdit,
  onDelete
}: {
  account: Account;
  events: AccountEvent[];
  busy: boolean;
  onActivate: () => void;
  onRelogin: () => void;
  onProbe: () => void;
  onReceiveCredit: () => void;
  onToggleEnabled: () => void;
  onResetHealth: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <>
      <div className="panel">
        <div className="detail-head">
          <div className="grow">
            <div className="detail-name">{account.name}</div>
            <div className="detail-sub">
              {account.nickname ?? "未获取昵称"}
              {account.userId ? ` · uid ${account.userId}` : ""}
            </div>
            <div className="row" style={{ marginTop: 9, flexWrap: "wrap" }}>
              <HealthBadge health={account.health} />
              {account.isActive ? <span className="badge badge-accent">当前账号</span> : null}
              {account.membership.isVip ? (
                <span className="badge badge-vip">
                  VIP
                  {account.membership.vipExpireAt ? ` · ${account.membership.vipExpireAt}` : ""}
                </span>
              ) : null}
              {account.enabled ? null : <span className="badge badge-gray">已停用</span>}
              {account.proxyEnabled ? <span className="badge badge-gray">独立代理</span> : null}
              {account.tags.map((tag) => (
                <span key={tag} className="chip">
                  {tag}
                </span>
              ))}
            </div>
          </div>
          {busy ? <span className="spin" style={{ color: "var(--accent)", fontSize: 18 }}>◌</span> : null}
        </div>

        {account.statusError ? (
          <div className="alert alert-error">
            <strong>最近一次错误：</strong>
            {account.statusError}
          </div>
        ) : null}

        {!account.enabled ? (
          <div className="alert alert-warn">该账号已停用，不会参与自动调度与保活巡检。</div>
        ) : null}
        {account.health === "expired" && account.enabled ? (
          <div className="alert alert-warn">
            登录态已失效。点下面的「重新登录」——管理器会弹一个浏览器窗口，
            你在里面正常登录一次，凭据会自动保存，不用手动复制 cookie。
          </div>
        ) : null}

        <div className="toolbar">
          <button className="btn btn-primary btn-sm" disabled={busy || account.isActive || !account.enabled} onClick={onActivate}>
            {account.isActive ? "已是当前账号" : "切换为当前"}
          </button>
          <button className="btn btn-sm" disabled={busy} onClick={onRelogin}>
            重新登录
          </button>
          <button className="btn btn-sm" disabled={busy} onClick={onProbe}>
            立即探活
          </button>
          <button className="btn btn-sm" disabled={busy} onClick={onReceiveCredit}>
            领取每日积分
          </button>
          <button className="btn btn-sm" disabled={busy} onClick={onToggleEnabled}>
            {account.enabled ? "停用" : "启用"}
          </button>
          <button className="btn btn-sm" disabled={busy} onClick={onResetHealth}>
            恢复状态
          </button>
          <span className="spacer" />
          <button className="btn btn-sm" disabled={busy} onClick={onEdit}>
            编辑
          </button>
          <button className="btn btn-sm btn-danger" disabled={busy} onClick={onDelete}>
            删除
          </button>
        </div>
      </div>

      <div className="panel">
        <div className="panel-title">积分</div>
        <div className="credit-row">
          <CreditPill label="总计" value={account.totalCredit} />
          <CreditPill label="赠送额度" value={account.freeCredit} />
          <CreditPill label="购买" value={account.purchaseCredit} />
          <CreditPill label="会员" value={account.vipCredit} />
        </div>
        <div className="field-hint" style={{ marginTop: 9 }}>
          状态更新于 {account.statusCheckedAt ? relTime(account.statusCheckedAt) : "尚未检测"}
        </div>
      </div>

      <div className="panel">
        <div className="panel-title">账号信息</div>
        <div className="kv">
          <KV label="凭据类型" value={account.credentialKind === "cookie" ? "完整 Cookie" : "sessionid"} />
          <KV label="凭据指纹" value={account.credentialFingerprint} mono />
          <KV
            label="独立代理"
            value={account.proxyEnabled ? account.proxyUrlMasked : "未启用"}
            mono={account.proxyEnabled}
          />
          {account.provider === "dreamina" ? (
            <KV
              label="区域集群"
              value={account.storeIdc ? `${account.storeCountry.toUpperCase()} · ${account.storeIdc}` : "未识别"}
            />
          ) : null}
          <KV label="添加时间" value={relTime(account.createdAt)} />
          <KV label="最近探活" value={relTime(account.statusCheckedAt)} />
          <KV label="最近使用" value={relTime(account.lastUsedAt)} />
          <KV label="最近成功" value={relTime(account.lastSuccessAt)} />
          <KV label="最近失败" value={relTime(account.lastFailureAt)} />
          <KV label="成功 / 失败" value={`${account.successCount} / ${account.failureCount}`} />
          <KV label="冷却截止" value={account.cooldownUntil ? relTime(account.cooldownUntil) : "—"} />
        </div>
        {account.remark ? (
          <div style={{ marginTop: 13 }}>
            <div className="kv-label">备注</div>
            <div style={{ fontSize: 13 }}>{account.remark}</div>
          </div>
        ) : null}
      </div>

      <div className="panel">
        <div className="panel-title">操作记录</div>
        {events.length === 0 ? (
          <div style={{ color: "var(--muted)", fontSize: 12.5, padding: "6px 0" }}>暂无记录</div>
        ) : (
          <div className="timeline">
            {events.map((event) => (
              <div className="tl-item" key={event.id}>
                <div className="tl-rail">
                  <span
                    className="tl-dot"
                    style={{ background: EVENT_TONE[event.level] ?? "var(--muted)" }}
                  />
                </div>
                <div className="tl-body">
                  <div className="tl-msg">{event.message}</div>
                  {event.detail ? (
                    <div className="mono" style={{ marginTop: 2, wordBreak: "break-all" }}>
                      {event.detail}
                    </div>
                  ) : null}
                  <div className="tl-time">
                    {event.kind} · {new Date(event.created_at).toLocaleString("zh-CN")}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </>
  );
}
