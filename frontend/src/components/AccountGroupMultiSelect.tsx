import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Loader2, Plus } from "lucide-react";
import type { AccountGroup } from "../types";

const FALLBACK_GROUP_COLOR = "#2563eb";
const DROPDOWN_GAP = 6;
const DROPDOWN_MAX_HEIGHT = 320;
const VIEWPORT_PADDING = 8;

interface DropdownPosition {
  top: number;
  left: number;
  width: number;
  maxHeight: number;
  openUp: boolean;
}

function normalizeGroupColor(color?: string): string {
  const value = (color || "").trim();
  return /^#[0-9a-fA-F]{6}$/.test(value) ? value : FALLBACK_GROUP_COLOR;
}

function resolveGroups(ids: number[], groups: AccountGroup[]): AccountGroup[] {
  if (ids.length === 0 || groups.length === 0) return [];
  const byID = new Map(groups.map((group) => [group.id, group]));
  return ids.map((id) => byID.get(id)).filter(Boolean) as AccountGroup[];
}

// Radix Dialog remove-scroll blocks native wheel on portaled menus; drive scrollTop manually.
function applyManualScroll(el: HTMLElement, deltaX: number, deltaY: number): boolean {
  const canScrollY = el.scrollHeight > el.clientHeight + 1;
  const canScrollX = el.scrollWidth > el.clientWidth + 1;
  if (!canScrollY && !canScrollX) return false;

  let scrolled = false;
  if (canScrollY && deltaY !== 0) {
    const maxTop = el.scrollHeight - el.clientHeight;
    const next = Math.min(maxTop, Math.max(0, el.scrollTop + deltaY));
    if (next !== el.scrollTop) {
      el.scrollTop = next;
      scrolled = true;
    }
  }
  if (canScrollX && deltaX !== 0) {
    const maxLeft = el.scrollWidth - el.clientWidth;
    const next = Math.min(maxLeft, Math.max(0, el.scrollLeft + deltaX));
    if (next !== el.scrollLeft) {
      el.scrollLeft = next;
      scrolled = true;
    }
  }
  return scrolled;
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
  /** Create a group by name; return the new group id so it can be auto-selected. */
  onCreateGroup?: (name: string) => Promise<number | null>;
  createLabel?: string;
  createPlaceholder?: string;
  creatingLabel?: string;
  createEmptyHint?: string;
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
  onCreateGroup,
  createLabel = "Create group",
  createPlaceholder = "New group name",
  creatingLabel = "Creating…",
  createEmptyHint,
}: AccountGroupMultiSelectProps) {
  const [open, setOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [creating, setCreating] = useState(false);
  const [position, setPosition] = useState<DropdownPosition | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const createInputRef = useRef<HTMLInputElement>(null);
  const touchStartRef = useRef<{ x: number; y: number } | null>(null);

  const selectedGroups = useMemo(
    () => resolveGroups(value, groups),
    [groups, value],
  );
  const missingCount = Math.max(0, value.length - selectedGroups.length);
  const visibleGroups = selectedGroups.slice(0, 3);
  const hiddenCount =
    selectedGroups.length - visibleGroups.length + missingCount;
  const summary = value.length === 0 ? allLabel || placeholder : selectedLabel;
  const canCreate = Boolean(onCreateGroup) && !disabled;
  const createNameTrimmed = createName.trim();

  const computePosition = useCallback(() => {
    // Anchor to the whole control (trigger + in-dialog create form) so the
    // portaled list does not cover the create input below the trigger.
    const anchor = rootRef.current ?? triggerRef.current;
    if (!anchor) return;
    const rect = anchor.getBoundingClientRect();
    const viewportHeight = window.innerHeight;
    const viewportWidth = window.innerWidth;
    const spaceBelow =
      viewportHeight - rect.bottom - DROPDOWN_GAP - VIEWPORT_PADDING;
    const spaceAbove = rect.top - DROPDOWN_GAP - VIEWPORT_PADDING;
    const preferUp =
      spaceBelow < Math.min(DROPDOWN_MAX_HEIGHT, 160) && spaceAbove > spaceBelow;
    const maxHeight = Math.max(
      120,
      Math.min(DROPDOWN_MAX_HEIGHT, preferUp ? spaceAbove : spaceBelow),
    );
    const width = Math.min(
      Math.max(rect.width, 240),
      viewportWidth - VIEWPORT_PADDING * 2,
    );
    const maxLeft = viewportWidth - width - VIEWPORT_PADDING;
    const left = Math.min(
      Math.max(VIEWPORT_PADDING, rect.left),
      Math.max(VIEWPORT_PADDING, maxLeft),
    );
    setPosition({
      top: preferUp ? rect.top - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
      left,
      width,
      maxHeight,
      openUp: preferUp,
    });
  }, []);

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    computePosition();
  }, [open, computePosition, groups.length, value.length]);

  useEffect(() => {
    if (!open) return;

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (rootRef.current?.contains(target)) return;
      if (dropdownRef.current?.contains(target)) return;
      setOpen(false);
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        // Don't steal Escape while the user is typing a new group name.
        if (
          event.target instanceof HTMLElement &&
          rootRef.current?.contains(event.target) &&
          (event.target.tagName === "INPUT" || event.target.tagName === "TEXTAREA")
        ) {
          return;
        }
        event.stopPropagation();
        setOpen(false);
      }
    };

    const handleReposition = () => computePosition();

    const handleWheel = (event: WheelEvent) => {
      const list = listRef.current;
      if (!list) return;
      const target = event.target as Node | null;
      if (!target || !list.contains(target)) return;
      if (applyManualScroll(list, event.deltaX, event.deltaY)) {
        event.preventDefault();
      }
    };

    const handleTouchStart = (event: TouchEvent) => {
      const list = listRef.current;
      if (!list) return;
      const target = event.target as Node | null;
      if (!target || !list.contains(target)) return;
      const touch = event.touches[0];
      if (!touch) return;
      touchStartRef.current = { x: touch.clientX, y: touch.clientY };
    };

    const handleTouchMove = (event: TouchEvent) => {
      const list = listRef.current;
      const start = touchStartRef.current;
      if (!list || !start) return;
      const target = event.target as Node | null;
      if (!target || !list.contains(target)) return;
      const touch = event.touches[0];
      if (!touch) return;
      const deltaX = start.x - touch.clientX;
      const deltaY = start.y - touch.clientY;
      touchStartRef.current = { x: touch.clientX, y: touch.clientY };
      if (applyManualScroll(list, deltaX, deltaY)) {
        event.preventDefault();
      }
    };

    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleEscape);
    window.addEventListener("resize", handleReposition);
    window.addEventListener("scroll", handleReposition, true);
    document.addEventListener("wheel", handleWheel, {
      passive: false,
      capture: true,
    });
    document.addEventListener("touchstart", handleTouchStart, {
      passive: true,
      capture: true,
    });
    document.addEventListener("touchmove", handleTouchMove, {
      passive: false,
      capture: true,
    });

    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleEscape);
      window.removeEventListener("resize", handleReposition);
      window.removeEventListener("scroll", handleReposition, true);
      document.removeEventListener("wheel", handleWheel, true);
      document.removeEventListener("touchstart", handleTouchStart, true);
      document.removeEventListener("touchmove", handleTouchMove, true);
      touchStartRef.current = null;
    };
  }, [open, computePosition]);

  const toggleOption = (id: number) => {
    if (disabled) return;
    if (value.includes(id)) {
      onChange(value.filter((item) => item !== id));
      return;
    }
    onChange([...value, id].sort((a, b) => a - b));
  };

  const handleCreate = async () => {
    if (!onCreateGroup || !createNameTrimmed || creating || disabled) return;
    setCreating(true);
    try {
      const id = await onCreateGroup(createNameTrimmed);
      if (id == null) return;
      if (!value.includes(id)) {
        onChange([...value, id].sort((a, b) => a - b));
      }
      setCreateName("");
      // Keep the create field ready for the next group.
      window.setTimeout(() => {
        createInputRef.current?.focus();
        if (open) computePosition();
      }, 0);
    } finally {
      setCreating(false);
    }
  };

  // Create form lives in the dialog DOM (not the body portal). Radix Dialog
  // focus-traps + aria-hides body siblings, so inputs outside content cannot type.
  const createForm = canCreate ? (
    <div className="mt-2 rounded-md border border-border bg-muted/15 p-2.5">
      {groups.length === 0 ? (
        <div className="mb-2 px-0.5 text-xs text-muted-foreground">
          {createEmptyHint || emptyLabel}
        </div>
      ) : null}
      <div className="flex items-center gap-2">
        <input
          ref={createInputRef}
          type="text"
          value={createName}
          disabled={creating || disabled}
          maxLength={80}
          placeholder={createPlaceholder}
          className="min-w-0 flex-1 rounded-md border border-input bg-background px-2.5 py-2 text-sm outline-none transition-[border-color,box-shadow] placeholder:text-muted-foreground focus:border-primary/40 focus:ring-[3px] focus:ring-primary/10 disabled:opacity-60"
          onChange={(event) => setCreateName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              event.stopPropagation();
              void handleCreate();
            }
          }}
        />
        <button
          type="button"
          disabled={creating || disabled || !createNameTrimmed}
          className="inline-flex shrink-0 items-center gap-1 rounded-md bg-primary px-2.5 py-2 text-xs font-semibold text-primary-foreground transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
          onClick={() => void handleCreate()}
        >
          {creating ? (
            <Loader2 className="size-3.5 animate-spin" />
          ) : (
            <Plus className="size-3.5" />
          )}
          {creating ? creatingLabel : createLabel}
        </button>
      </div>
    </div>
  ) : null;

  const dropdown =
    open && position
      ? createPortal(
          <div
            ref={dropdownRef}
            data-select-dropdown="true"
            className="pointer-events-auto fixed z-[1000] overflow-hidden rounded-lg border border-border bg-popover shadow-[0_18px_40px_hsl(222_30%_18%/0.16)] backdrop-blur-sm"
            style={
              position.openUp
                ? {
                    left: position.left,
                    width: position.width,
                    bottom: window.innerHeight - position.top,
                    maxHeight: position.maxHeight,
                  }
                : {
                    left: position.left,
                    width: position.width,
                    top: position.top,
                    maxHeight: position.maxHeight,
                  }
            }
          >
            {groups.length === 0 ? (
              <div className="px-4 py-3 text-sm text-muted-foreground">
                {canCreate ? createEmptyHint || emptyLabel : emptyLabel}
              </div>
            ) : (
              <div
                ref={listRef}
                className="max-h-inherit space-y-1 overflow-y-auto overscroll-contain p-2 [scrollbar-width:thin]"
                style={{ maxHeight: position.maxHeight }}
                role="listbox"
              >
                {groups.map((group) => {
                  const checked = value.includes(group.id);
                  const color = normalizeGroupColor(group.color);
                  return (
                    <button
                      key={group.id}
                      type="button"
                      role="option"
                      aria-selected={checked}
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
          </div>,
          document.body,
        )
      : null;

  return (
    <div ref={rootRef} className="relative">
      <button
        ref={triggerRef}
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
      {/* In-dialog create field — must stay inside Modal/Dialog DOM for focus. */}
      {createForm}
      {dropdown}
    </div>
  );
}
