import { useEffect, useState, type MouseEvent as ReactMouseEvent } from "react";
import { Loader2, PowerOff, RotateCcw, ShieldAlert } from "lucide-react";
import type { TFunction } from "i18next";
import { cn } from "@/lib/utils";
import type { AccountRow } from "../types";
import { formatBeijingTime } from "../utils/time";
import {
  isDisabledAccountOverlayAccount,
  resolveAccountOverlayKind,
} from "../lib/accountStateOverlay";
import type { AccountOverlayKind } from "../lib/accountStateOverlay";

export {
  accountStateSurfaceClass,
  accountStateTableRowClass,
  disabledAccountSurfaceClass,
  disabledAccountTableRowClass,
  isDisabledAccountOverlayAccount,
  resolveAccountOverlayKind,
} from "../lib/accountStateOverlay";
export type { AccountOverlayKind } from "../lib/accountStateOverlay";

function formatCountdownRemaining(untilMs: number, nowMs: number): string {
  const diff = Math.max(0, untilMs - nowMs);
  if (diff <= 0) return "";
  const hours = Math.floor(diff / 3600000);
  const minutes = Math.floor((diff % 3600000) / 60000);
  const seconds = Math.floor((diff % 60000) / 1000);
  if (hours > 0) {
    return `${hours}h ${String(minutes).padStart(2, "0")}m ${String(seconds).padStart(2, "0")}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
  }
  return `${seconds}s`;
}

function useCountdownRemaining(until?: string): string {
  const [remaining, setRemaining] = useState("");

  useEffect(() => {
    if (!until) {
      setRemaining("");
      return;
    }
    const target = new Date(until).getTime();
    const update = () => {
      setRemaining(formatCountdownRemaining(target, Date.now()));
    };
    update();
    const id = setInterval(update, 1000);
    return () => clearInterval(id);
  }, [until]);

  return remaining;
}

export function AccountStateOverlay({
  kind,
  label,
  compact = false,
  markerOnly = false,
  countdownUntil,
  resumingLabel,
  recoverLabel,
  recoveringLabel,
  onRecover,
}: {
  kind: AccountOverlayKind;
  label: string;
  compact?: boolean;
  markerOnly?: boolean;
  countdownUntil?: string;
  resumingLabel?: string;
  recoverLabel?: string;
  recoveringLabel?: string;
  onRecover?: () => void | Promise<void>;
}) {
  const remaining = useCountdownRemaining(countdownUntil);
  const [busy, setBusy] = useState(false);
  const isOverload = kind === "overload";
  const countdownText = remaining || (isOverload ? resumingLabel : undefined);

  const handleRecover = async (event: ReactMouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    if (!onRecover || busy) return;
    setBusy(true);
    try {
      await onRecover();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className={cn(
        "account-state-overlay pointer-events-none",
        markerOnly
          ? "account-state-overlay--marker-only w-full"
          : "absolute inset-0 z-10 overflow-hidden rounded-[inherit]",
        isOverload
          ? "account-state-overlay--overload"
          : "account-state-overlay--disabled",
      )}
      aria-hidden={markerOnly ? undefined : false}
    >
      {markerOnly ? null : (
        <div className="account-state-overlay__scrim absolute inset-0" />
      )}
      <div
        className={cn(
          "account-state-overlay__mark flex items-center justify-center",
          markerOnly
            ? "account-state-overlay__mark--inline min-h-6 w-full"
            : "absolute inset-0 px-3",
        )}
      >
        {isOverload && !compact ? (
          <div className="flex min-w-[188px] flex-col items-center gap-2 rounded-2xl border border-orange-300/60 bg-card/95 px-4 py-3 text-orange-800 shadow-[0_1px_2px_hsl(24_80%_20%/0.06),0_10px_28px_hsl(24_80%_20%/0.08)] backdrop-blur-md dark:border-orange-400/25 dark:text-orange-100 dark:shadow-[0_1px_2px_rgb(0_0_0/0.24),0_10px_28px_rgb(0_0_0/0.2)]">
            <div className="flex items-center gap-1.5">
              <span className="flex size-5 items-center justify-center rounded-full bg-orange-500/15 text-orange-600 dark:text-orange-300">
                <ShieldAlert className="size-3" />
              </span>
              <span className="text-xs font-medium tracking-[0.04em]">
                {label}
              </span>
            </div>
            {countdownText ? (
              <span
                className="font-mono text-lg font-semibold leading-none tabular-nums tracking-wide"
                title={countdownUntil ? formatBeijingTime(countdownUntil) : undefined}
              >
                {countdownText}
              </span>
            ) : null}
            {onRecover ? (
              <button
                type="button"
                disabled={busy}
                onClick={(event) => void handleRecover(event)}
                className="pointer-events-auto inline-flex items-center gap-1 rounded-full bg-orange-600 px-2.5 py-1 text-[11px] font-medium text-white shadow-sm transition-colors hover:bg-orange-500 disabled:cursor-wait disabled:opacity-70 dark:bg-orange-500 dark:hover:bg-orange-400"
              >
                {busy ? (
                  <Loader2 className="size-3 animate-spin" />
                ) : (
                  <RotateCcw className="size-3" />
                )}
                {busy ? recoveringLabel : recoverLabel}
              </button>
            ) : null}
          </div>
        ) : (
          <span
            className={cn(
              "inline-flex max-w-full flex-wrap items-center justify-center rounded-full border bg-card/95 shadow-[0_1px_2px_hsl(222_40%_11%/0.06),0_8px_24px_hsl(222_40%_11%/0.06)] backdrop-blur-md dark:shadow-[0_1px_2px_rgb(0_0_0/0.24),0_8px_24px_rgb(0_0_0/0.18)]",
              compact && isOverload
                ? "gap-2 py-1.5 pl-2 pr-2.5"
                : compact
                  ? "gap-1 py-0.5 pl-1 pr-1.5"
                  : "gap-1.5 py-1 pl-1.5 pr-2.5",
              isOverload
                ? "border-orange-300/70 text-orange-800 dark:border-orange-400/25 dark:text-orange-100"
                : "border-border/60 text-muted-foreground",
            )}
          >
            <span
              className={cn(
                "flex items-center justify-center rounded-full",
                compact && isOverload ? "size-6" : compact ? "size-4" : "size-5",
                isOverload
                  ? "bg-orange-500/15 text-orange-600 dark:text-orange-300"
                  : "bg-muted/80 text-muted-foreground",
              )}
            >
              {isOverload ? (
                <ShieldAlert className={compact ? "size-3.5" : "size-3"} />
              ) : (
                <PowerOff className={compact ? "size-2.5" : "size-3"} />
              )}
            </span>
            <span
              className={cn(
                "whitespace-nowrap font-medium tracking-[0.04em]",
                compact && isOverload ? "text-sm" : compact ? "text-[11px]" : "text-xs",
              )}
            >
              {label}
            </span>
            {countdownText ? (
              <span
                className={cn(
                  "whitespace-nowrap font-mono font-semibold tabular-nums tracking-wide",
                  compact && isOverload ? "text-sm" : compact ? "text-[11px]" : "text-xs",
                )}
                title={countdownUntil ? formatBeijingTime(countdownUntil) : undefined}
              >
                {countdownText}
              </span>
            ) : null}
            {isOverload && onRecover ? (
              <button
                type="button"
                disabled={busy}
                onClick={(event) => void handleRecover(event)}
                className={cn(
                  "pointer-events-auto inline-flex items-center whitespace-nowrap rounded-full bg-orange-600 font-medium text-white shadow-sm transition-colors hover:bg-orange-500 disabled:cursor-wait disabled:opacity-70 dark:bg-orange-500 dark:hover:bg-orange-400",
                  compact ? "gap-1.5 px-2.5 py-1 text-xs" : "gap-1 px-2 py-0.5 text-[11px]",
                )}
              >
                {busy ? (
                  <Loader2 className={compact ? "size-3.5 animate-spin" : "size-3 animate-spin"} />
                ) : (
                  <RotateCcw className={compact ? "size-3.5" : "size-3"} />
                )}
                {busy ? recoveringLabel : recoverLabel}
              </button>
            ) : null}
          </span>
        )}
      </div>
    </div>
  );
}

export function renderAccountStateOverlay(
  account: AccountRow,
  t: TFunction,
  options: {
    compact?: boolean;
    markerOnly?: boolean;
    onRecover?: () => void | Promise<void>;
  } = {},
) {
  const kind = resolveAccountOverlayKind(account);
  if (!kind) return null;
  return (
    <AccountStateOverlay
      kind={kind}
      compact={options.compact}
      markerOnly={options.markerOnly}
      label={
        kind === "disabled"
          ? t("accounts.disabledOverlay")
          : t("accounts.overloadOverlay")
      }
      countdownUntil={
        kind === "overload" ? account.cooldown_until : undefined
      }
      resumingLabel={t("accounts.overloadResuming")}
      recoverLabel={t("accounts.overloadRecover")}
      recoveringLabel={t("accounts.overloadRecovering")}
      onRecover={kind === "overload" ? options.onRecover : undefined}
    />
  );
}

export function renderDisabledAccountOverlay(
  account: AccountRow,
  t: TFunction,
  options: { compact?: boolean; markerOnly?: boolean } = {},
) {
  if (!isDisabledAccountOverlayAccount(account)) return null;
  return (
    <AccountStateOverlay
      kind="disabled"
      compact={options.compact}
      markerOnly={options.markerOnly}
      label={t("accounts.disabledOverlay")}
    />
  );
}
