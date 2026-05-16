import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ChangeEvent,
} from "react";
import { X, ChevronDown } from "lucide-react";

export interface ChipInputProps {
  value: string[];
  onChange: (next: string[]) => void;
  /** Pre-defined options for select-from-list mode */
  options?: string[];
  placeholder?: string;
  disabled?: boolean;
  maxVisible?: number;
  className?: string;
}

/**
 * Reusable multi-select chip input supporting:
 * - Free-text tag entry (type + Enter/comma to add)
 * - Select-from-list mode (with options prop)
 * - Chips with X to remove
 * - Max N visible chips + "+N" overflow badge
 */
export default function ChipInput({
  value,
  onChange,
  options,
  placeholder = "",
  disabled = false,
  maxVisible = 3,
  className = "",
}: ChipInputProps) {
  const [draft, setDraft] = useState("");
  const [showDropdown, setShowDropdown] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const hasOptions = Array.isArray(options) && options.length > 0;

  const availableOptions = useMemo(() => {
    if (!hasOptions) return [];
    const selected = new Set(value.map((v) => v.toLowerCase()));
    return options!.filter((opt) => !selected.has(opt.toLowerCase()));
  }, [hasOptions, options, value]);

  const addChip = useCallback(
    (tag: string) => {
      const trimmed = tag.trim();
      if (!trimmed) return;
      const lower = trimmed.toLowerCase();
      if (value.some((v) => v.toLowerCase() === lower)) return;
      onChange([...value, trimmed]);
      setDraft("");
      setShowDropdown(false);
    },
    [value, onChange],
  );

  const removeChip = useCallback(
    (index: number) => {
      const next = [...value];
      next.splice(index, 1);
      onChange(next);
    },
    [value, onChange],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (disabled) return;
      if (e.key === "Enter" || e.key === ",") {
        e.preventDefault();
        if (draft.trim()) {
          addChip(draft);
        }
      } else if (e.key === "Backspace" && !draft && value.length > 0) {
        removeChip(value.length - 1);
      }
    },
    [disabled, draft, addChip, removeChip, value.length],
  );

  const handleChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      const v = e.target.value;
      if (v.includes(",")) {
        const parts = v.split(",");
        const existing = new Set(value.map((item) => item.toLowerCase()));
        const toAdd: string[] = [];
        for (let i = 0; i < parts.length - 1; i++) {
          const trimmed = parts[i].trim();
          if (!trimmed) continue;
          const lowered = trimmed.toLowerCase();
          if (existing.has(lowered)) continue;
          existing.add(lowered);
          toAdd.push(trimmed);
        }
        if (toAdd.length > 0) {
          onChange([...value, ...toAdd]);
        }
        setDraft(parts[parts.length - 1]);
      } else {
        setDraft(v);
      }
      if (hasOptions) setShowDropdown(true);
    },
    [hasOptions, onChange, value],
  );

  // Close dropdown on outside click
  useEffect(() => {
    if (!showDropdown) return;
    const handler = (e: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setShowDropdown(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showDropdown]);

  const visibleChips = value.slice(0, maxVisible);
  const overflowCount = value.length - maxVisible;

  return (
    <div ref={containerRef} className={`relative ${className}`}>
      <div
        className={`flex flex-wrap items-center gap-1.5 rounded-md border border-input bg-background px-2 py-1.5 text-sm
          ${disabled ? "opacity-50 cursor-not-allowed" : "cursor-text"}
          focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-1`}
        onClick={() => inputRef.current?.focus()}
      >
        {visibleChips.map((chip, i) => (
          <span
            key={`${chip}-${i}`}
            className="inline-flex items-center gap-1 rounded-md bg-primary/10 text-primary px-2 py-0.5 text-xs font-medium"
          >
            {chip}
            {!disabled && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  removeChip(i);
                }}
                className="ml-0.5 rounded-full hover:bg-primary/20 p-0.5 transition-colors"
                aria-label={`Remove ${chip}`}
              >
                <X className="size-3" />
              </button>
            )}
          </span>
        ))}
        {overflowCount > 0 && (
          <span className="inline-flex items-center rounded-md bg-muted text-muted-foreground px-2 py-0.5 text-xs font-medium">
            +{overflowCount}
          </span>
        )}
        <input
          ref={inputRef}
          type="text"
          value={draft}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          onFocus={() => {
            if (hasOptions) setShowDropdown(true);
          }}
          placeholder={value.length === 0 ? placeholder : ""}
          disabled={disabled}
          className="flex-1 min-w-[80px] bg-transparent outline-none text-sm placeholder:text-muted-foreground disabled:cursor-not-allowed"
        />
        {hasOptions && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              setShowDropdown(!showDropdown);
              inputRef.current?.focus();
            }}
            className="shrink-0 p-0.5 text-muted-foreground hover:text-foreground transition-colors"
            tabIndex={-1}
          >
            <ChevronDown
              className={`size-4 transition-transform ${showDropdown ? "rotate-180" : ""}`}
            />
          </button>
        )}
      </div>

      {/* Dropdown for select-from-list mode */}
      {hasOptions && showDropdown && availableOptions.length > 0 && (
        <div className="absolute z-50 mt-1 w-full max-h-48 overflow-auto rounded-md border border-border bg-popover shadow-md">
          {availableOptions.map((opt) => (
            <button
              key={opt}
              type="button"
              className="w-full text-left px-3 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground transition-colors"
              onMouseDown={(e) => {
                e.preventDefault();
                addChip(opt);
              }}
            >
              {opt}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
