export const OPERATION_RESULTS_VISIBILITY_STORAGE_KEY =
  "codex2api:accounts:show-operation-results";

type PreferenceStorage = Pick<Storage, "getItem" | "setItem">;

function resolveStorage(storage?: PreferenceStorage): PreferenceStorage | null {
  if (storage) return storage;
  if (typeof window === "undefined") return null;
  return window.localStorage;
}

export function readOperationResultsVisibility(
  storage?: PreferenceStorage,
): boolean {
  try {
    return (
      resolveStorage(storage)?.getItem(
        OPERATION_RESULTS_VISIBILITY_STORAGE_KEY,
      ) === "true"
    );
  } catch {
    return false;
  }
}

export function writeOperationResultsVisibility(
  visible: boolean,
  storage?: PreferenceStorage,
): void {
  try {
    resolveStorage(storage)?.setItem(
      OPERATION_RESULTS_VISIBILITY_STORAGE_KEY,
      visible ? "true" : "false",
    );
  } catch {
    // Keep the in-memory preference working when localStorage is unavailable.
  }
}
