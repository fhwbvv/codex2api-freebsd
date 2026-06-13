import { useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronDown } from "lucide-react";
import type { AccountGroup } from "../types";

const FALLBACK_GROUP_COLOR = "#2563eb";

function normalizeGroupColor(color?: string): string {
  const value = (color || "").trim();
  return /^#[0-9a-fA-F]{6}$/.test(value) ? value : FALLBACK_GROUP_COLOR;
}

function resolveGroups(ids: number[], groups: AccountGroup[]): AccountGroup[] {
  if (ids.length === 0 || groups.length === 0) return [];
  const byID = new Map(groups.map((group) => [group.id, group]));
  return ids.map((id) => byID.get(id)).filter(Boolean) as AccountGroup[];
}

export interface AccountGroupMultiSelectProps {
  groups: AccountGroup[];
  value: number[];
  onChange: (value: number[]) => void;
  placeholder: string;
  emptyLabel: string;
  emptyHint?: string;
  allLabel?: string;
  selectedLabel: string;
  disabled?: boolean;
}

export default function AccountGroupMultiSelect({
  groups,
  value,
  onChange,
  placeholder,
  emptyLabel,
  emptyHint,
  allLabel,
  selectedLabel,
  disabled = false,
}: AccountGroupMultiSelectProps) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;

    const handlePointerDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };

    document.addEventListener("mousedown", handlePointerDown);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("mousedown", handlePointerDown);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [open]);

  const selectedGroups = useMemo(() => resolveGroups(value, groups), [groups, value]);
  const missingCount = Math.max(0, value.length - selectedGroups.length);
  const visibleGroups = selectedGroups.slice(0, 3);
  const hiddenCount = selectedGroups.length - visibleGroups.length + missingCount;
  const summary = value.length === 0 ? allLabel || placeholder : selectedLabel;

  const toggleOption = (id: number) => {
    if (disabled) return;
    if (value.includes(id)) {
      onChange(value.filter((item) => item !== id));
      return;
    }
    onChange([...value, id].sort((a, b) => a - b));
  };

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        className={`flex w-full items-center justify-between gap-3 rounded-md border border-input bg-background px-3.5 py-3 text-left shadow-xs transition-[border-color,box-shadow] ${
          disabled
            ? "cursor-not-allowed opacity-70"
            : "hover:border-primary/30 hover:bg-accent/40"
        } ${open ? "border-primary/35 ring-[3px] ring-primary/10" : ""}`}
        onClick={() => {
          if (!disabled) {
            setOpen((current) => !current);
          }
        }}
      >
        <div className="min-w-0 flex-1">
          <div className="truncate text-[15px] text-foreground">{summary}</div>
          <div className="mt-1 flex min-h-5 flex-wrap gap-1.5">
            {value.length === 0 ? (
              <span className="truncate text-xs text-muted-foreground">
                {emptyHint || placeholder}
              </span>
            ) : (
              <>
                {visibleGroups.map((group) => {
                  const color = normalizeGroupColor(group.color);
                  return (
                    <span
                      key={group.id}
                      className="inline-flex max-w-[10rem] items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-semibold"
                      style={{
                        backgroundColor: `${color}14`,
                        color,
                        boxShadow: `inset 0 0 0 1px ${color}33`,
                      }}
                      title={group.description || group.name}
                    >
                      <span className="size-1.5 shrink-0 rounded-full bg-current" />
                      <span className="truncate">{group.name}</span>
                    </span>
                  );
                })}
                {hiddenCount > 0 && (
                  <span className="inline-flex items-center rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-semibold text-muted-foreground">
                    +{hiddenCount}
                  </span>
                )}
              </>
            )}
          </div>
        </div>
        <ChevronDown
          className={`size-4 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>

      {open ? (
        <div className="absolute left-0 right-0 top-[calc(100%+0.5rem)] z-50 overflow-hidden rounded-lg border border-border bg-popover shadow-[0_18px_40px_hsl(222_30%_18%/0.12)] backdrop-blur-sm">
          {groups.length === 0 ? (
            <div className="px-4 py-3 text-sm text-muted-foreground">
              {emptyLabel}
            </div>
          ) : (
            <div className="max-h-72 space-y-1 overflow-auto p-2">
              {groups.map((group) => {
                const checked = value.includes(group.id);
                const color = normalizeGroupColor(group.color);
                return (
                  <button
                    key={group.id}
                    type="button"
                    className={`flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left transition-colors ${
                      checked
                        ? "bg-primary/10 text-primary"
                        : "text-foreground hover:bg-accent/70"
                    }`}
                    onClick={() => toggleOption(group.id)}
                  >
                    <span
                      className={`flex size-4 shrink-0 items-center justify-center rounded border ${checked ? "border-primary bg-primary text-primary-foreground" : "border-border bg-background text-transparent"}`}
                    >
                      <Check className="size-3" />
                    </span>
                    <span
                      className="size-2.5 shrink-0 rounded-full"
                      style={{ backgroundColor: color }}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">
                        {group.name}
                      </span>
                      {group.description ? (
                        <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                          {group.description}
                        </span>
                      ) : null}
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}
