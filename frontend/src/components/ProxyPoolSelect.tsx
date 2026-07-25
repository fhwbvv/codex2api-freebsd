import { useTranslation } from "react-i18next";

import type { ProxyRow } from "../api";
import { Select, type SelectOption } from "./ui/select";

interface ProxyPoolSelectProps {
  proxies: ProxyRow[];
  onSelect: (url: string) => void;
  disabled?: boolean;
  className?: string;
}

// ProxyPoolSelect 是账号表单里"从代理池选一条代理填入代理输入框"的下拉。
// 它不持有选中状态——选中后把该代理的 URL 交给 onSelect（由上层写进代理输入框，
// 仍可手动编辑），下拉本身回到占位符。代理池为空时不渲染（无可选项）。
export function ProxyPoolSelect({
  proxies,
  onSelect,
  disabled = false,
  className,
}: ProxyPoolSelectProps) {
  const { t } = useTranslation();
  if (proxies.length === 0) {
    return null;
  }
  const options: SelectOption[] = proxies.map((proxy) => {
    const label = proxy.label?.trim();
    return {
      value: proxy.url,
      label: label ? `${label} — ${proxy.url}` : proxy.url,
      triggerLabel: label || proxy.url,
    };
  });
  return (
    <Select
      compact
      className={className}
      value=""
      placeholder={t("proxies.selectFromPool")}
      disabled={disabled}
      options={options}
      onValueChange={(url) => {
        if (url.trim()) onSelect(url);
      }}
    />
  );
}
