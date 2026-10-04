import type { Account } from "../types.ts";
import { HEALTH_META, HealthBadge, relTime } from "./ui.tsx";

export function AccountCard({
  account,
  selected,
  busy,
  onSelect
}: {
  account: Account;
  selected: boolean;
  busy: boolean;
  onSelect: () => void;
}) {
  const credit = account.totalCredit;
  return (
    <div
      className={[
        "card",
        selected ? "selected" : "",
        account.isActive ? "is-active" : "",
        account.enabled ? "" : "disabled"
      ]
        .filter(Boolean)
        .join(" ")}
      onClick={onSelect}
    >
      <div className="card-head">
        <div className="grow" style={{ minWidth: 0 }}>
          <div className="card-name" title={account.name}>
            {account.name}
          </div>
          <div className="card-nick">
            {account.nickname ?? account.userId ?? "（未获取到昵称）"}
          </div>
        </div>
        {busy ? <span className="spin" style={{ color: "var(--accent)" }}>◌</span> : null}
        <HealthBadge health={account.health} />
      </div>

      <div className="card-meta">
        {account.isActive ? <span className="badge badge-accent">当前</span> : null}
        {account.membership.isVip ? <span className="badge badge-vip">VIP</span> : null}
        {!account.enabled ? <span className="badge badge-gray">已停用</span> : null}
        {account.proxyEnabled ? (
          <span className="badge badge-gray" title={account.proxyUrlMasked}>
            代理
          </span>
        ) : null}
        {credit !== null ? (
          <span className="chip">{credit} 积分</span>
        ) : (
          <span className="chip">积分未知</span>
        )}
        {account.inCooldown ? <span className="chip">冷却中</span> : null}
        <span className="spacer mono" style={{ fontSize: 11 }}>
          {account.statusCheckedAt ? relTime(account.statusCheckedAt) : HEALTH_META[account.health].label}
        </span>
      </div>
    </div>
  );
}
