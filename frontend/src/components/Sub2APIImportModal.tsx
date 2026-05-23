import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { getAdminKey } from "../api";
import Modal from "./Modal";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getErrorMessage } from "../utils/error";

type Scope = "available" | "healthy" | "all";

interface AccountSummary {
  name: string;
  chatgpt_account_id: string;
  email: string;
  plan_type: string;
  status: string;
  error_message?: string;
  rate_limited: boolean;
  available: boolean;
  healthy: boolean;
}

interface Sub2APIPreview {
  total: number;
  openai_total: number;
  available_count: number;
  healthy_count: number;
  rate_limited_count: number;
  error_count: number;
  other_platform: number;
  accounts: AccountSummary[];
}

interface Sub2APIImportModalProps {
  show: boolean;
  onClose: () => void;
  onImportStart: (res: Response) => Promise<void>;
  onShowToast: (message: string, kind?: "success" | "error") => void;
}

export default function Sub2APIImportModal({
  show,
  onClose,
  onImportStart,
  onShowToast,
}: Sub2APIImportModalProps) {
  const { t } = useTranslation();
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [previewing, setPreviewing] = useState(false);
  const [preview, setPreview] = useState<Sub2APIPreview | null>(null);
  const [importingScope, setImportingScope] = useState<Scope | null>(null);

  useEffect(() => {
    if (!show) {
      setPreview(null);
      setImportingScope(null);
      setPreviewing(false);
    }
  }, [show]);

  const fetchHeaders = (): HeadersInit => {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    const key = getAdminKey();
    if (key) headers["X-Admin-Key"] = key;
    return headers;
  };

  const handlePreview = async () => {
    setPreviewing(true);
    try {
      const res = await fetch("/api/admin/accounts/sub2api/preview", {
        method: "POST",
        headers: fetchHeaders(),
        body: JSON.stringify({ base_url: baseUrl, api_key: apiKey }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        onShowToast(
          t("accounts.sub2api.previewFailed", {
            error: data.error || res.statusText,
          }),
          "error",
        );
        return;
      }
      const data = (await res.json()) as Sub2APIPreview;
      setPreview(data);
    } catch (error) {
      onShowToast(
        t("accounts.sub2api.previewFailed", {
          error: getErrorMessage(error),
        }),
        "error",
      );
    } finally {
      setPreviewing(false);
    }
  };

  const handleImport = async (scope: Scope) => {
    setImportingScope(scope);
    try {
      const res = await fetch("/api/admin/accounts/sub2api/import", {
        method: "POST",
        headers: fetchHeaders(),
        body: JSON.stringify({
          base_url: baseUrl,
          api_key: apiKey,
          scope,
        }),
      });
      if (res.headers.get("content-type")?.includes("text/event-stream")) {
        // 关闭当前 modal,把 SSE 交回 Accounts 页面统一进度展示
        onClose();
        await onImportStart(res);
      } else {
        const data = await res.json().catch(() => ({}));
        if (!res.ok) {
          onShowToast(
            t("accounts.sub2api.importFailed", {
              error: data.error || res.statusText,
            }),
            "error",
          );
        } else {
          onShowToast(data.message || t("accounts.importCompleted"));
          onClose();
        }
      }
    } catch (error) {
      onShowToast(
        t("accounts.sub2api.importFailed", {
          error: getErrorMessage(error),
        }),
        "error",
      );
    } finally {
      setImportingScope(null);
    }
  };

  const isImporting = importingScope !== null;
  const canPreview = baseUrl.trim() !== "" && apiKey.trim() !== "" && !previewing;

  return (
    <Modal
      show={show}
      title={t("accounts.sub2api.title")}
      contentClassName="sm:max-w-[680px]"
      onClose={onClose}
    >
      <div className="space-y-4">
        <div className="space-y-2">
          <label className="text-sm font-medium">
            {t("accounts.sub2api.baseUrl")}
          </label>
          <Input
            type="text"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://your-sub2api.example.com"
            disabled={previewing || isImporting}
          />
          <p className="text-[11px] text-muted-foreground">
            {t("accounts.sub2api.baseUrlHint")}
          </p>
        </div>
        <div className="space-y-2">
          <label className="text-sm font-medium">
            {t("accounts.sub2api.apiKey")}
          </label>
          <Input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder={t("accounts.sub2api.apiKeyPlaceholder")}
            disabled={previewing || isImporting}
            autoComplete="off"
          />
          <p className="text-[11px] text-muted-foreground">
            {t("accounts.sub2api.apiKeyHint")}
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Button
            onClick={handlePreview}
            disabled={!canPreview || isImporting}
          >
            {previewing
              ? t("accounts.sub2api.previewing")
              : t("accounts.sub2api.preview")}
          </Button>
          {preview && (
            <span className="text-xs text-muted-foreground">
              {t("accounts.sub2api.lastFetched")}
            </span>
          )}
        </div>

        {preview && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <Stat
                label={t("accounts.sub2api.statTotal")}
                value={preview.openai_total}
                hint={
                  preview.other_platform > 0
                    ? t("accounts.sub2api.statOtherSkipped", {
                        count: preview.other_platform,
                      })
                    : undefined
                }
              />
              <Stat
                label={t("accounts.sub2api.statAvailable")}
                value={preview.available_count}
                tone="success"
              />
              <Stat
                label={t("accounts.sub2api.statRateLimited")}
                value={preview.rate_limited_count}
                tone="warning"
              />
              <Stat
                label={t("accounts.sub2api.statError")}
                value={preview.error_count}
                tone="danger"
              />
            </div>

            <div className="grid gap-2 sm:grid-cols-3">
              <Button
                variant="default"
                disabled={isImporting || preview.available_count === 0}
                onClick={() => void handleImport("available")}
              >
                {importingScope === "available"
                  ? t("accounts.sub2api.importing")
                  : t("accounts.sub2api.importAvailable", {
                      count: preview.available_count,
                    })}
              </Button>
              <Button
                variant="secondary"
                disabled={isImporting || preview.healthy_count === 0}
                onClick={() => void handleImport("healthy")}
              >
                {importingScope === "healthy"
                  ? t("accounts.sub2api.importing")
                  : t("accounts.sub2api.importHealthy", {
                      count: preview.healthy_count,
                    })}
              </Button>
              <Button
                variant="outline"
                disabled={isImporting || preview.openai_total === 0}
                onClick={() => void handleImport("all")}
              >
                {importingScope === "all"
                  ? t("accounts.sub2api.importing")
                  : t("accounts.sub2api.importAll", {
                      count: preview.openai_total,
                    })}
              </Button>
            </div>

            {preview.accounts.length > 0 && (
              <div className="max-h-72 overflow-y-auto rounded-lg border border-border">
                <table className="w-full text-xs">
                  <thead className="sticky top-0 bg-muted/50">
                    <tr className="text-left text-muted-foreground">
                      <th className="px-3 py-2 font-medium">
                        {t("accounts.sub2api.colName")}
                      </th>
                      <th className="px-3 py-2 font-medium">
                        {t("accounts.sub2api.colPlan")}
                      </th>
                      <th className="px-3 py-2 font-medium">
                        {t("accounts.sub2api.colStatus")}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {preview.accounts.slice(0, 200).map((a, idx) => (
                      <tr
                        key={`${a.chatgpt_account_id}-${idx}`}
                        className="border-t border-border/60"
                      >
                        <td className="px-3 py-1.5">
                          {a.name || a.email || a.chatgpt_account_id || "-"}
                        </td>
                        <td className="px-3 py-1.5 text-muted-foreground">
                          {a.plan_type || "-"}
                        </td>
                        <td className="px-3 py-1.5">
                          <StatusPill account={a} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {preview.accounts.length > 200 && (
                  <div className="px-3 py-2 text-[11px] text-muted-foreground">
                    {t("accounts.sub2api.listTruncated", {
                      shown: 200,
                      total: preview.accounts.length,
                    })}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}

function Stat({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: number;
  hint?: string;
  tone?: "success" | "warning" | "danger";
}) {
  const toneClass =
    tone === "success"
      ? "text-emerald-600 dark:text-emerald-400"
      : tone === "warning"
      ? "text-amber-600 dark:text-amber-400"
      : tone === "danger"
      ? "text-rose-600 dark:text-rose-400"
      : "text-foreground";
  return (
    <div className="rounded-lg border border-border px-3 py-2">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className={`text-lg font-semibold ${toneClass}`}>{value}</div>
      {hint && (
        <div className="mt-0.5 text-[10px] text-muted-foreground">{hint}</div>
      )}
    </div>
  );
}

function StatusPill({ account }: { account: AccountSummary }) {
  if (!account.healthy) {
    return (
      <span className="inline-flex rounded-full bg-rose-500/10 px-2 py-0.5 text-rose-600 dark:text-rose-400">
        {account.status || "error"}
      </span>
    );
  }
  if (account.rate_limited) {
    return (
      <span className="inline-flex rounded-full bg-amber-500/10 px-2 py-0.5 text-amber-600 dark:text-amber-400">
        rate_limited
      </span>
    );
  }
  return (
    <span className="inline-flex rounded-full bg-emerald-500/10 px-2 py-0.5 text-emerald-600 dark:text-emerald-400">
      active
    </span>
  );
}
