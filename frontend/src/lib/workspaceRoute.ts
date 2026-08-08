export const CHATGPT_ACCOUNT_ID_HEADER = "Chatgpt-Account-Id";

export function workspaceRouteFromHeaders(
  headers: Record<string, string> | null | undefined,
): string {
  for (const [name, value] of Object.entries(headers ?? {})) {
    if (name.trim().toLowerCase() === CHATGPT_ACCOUNT_ID_HEADER.toLowerCase()) {
      return value.trim();
    }
  }
  return "";
}

export function applyWorkspaceRouteHeader(
  headers: Record<string, string> | null | undefined,
  workspaceID: string,
): Record<string, string> | null {
  const next: Record<string, string> = {};
  for (const [name, value] of Object.entries(headers ?? {})) {
    if (name.trim().toLowerCase() === CHATGPT_ACCOUNT_ID_HEADER.toLowerCase()) {
      continue;
    }
    next[name] = value;
  }

  const normalizedWorkspaceID = workspaceID.trim();
  if (normalizedWorkspaceID) {
    next[CHATGPT_ACCOUNT_ID_HEADER] = normalizedWorkspaceID;
  }
  return Object.keys(next).length > 0 ? next : null;
}

export function applyOptionalWorkspaceRouteHeader(
  headers: Record<string, string> | null | undefined,
  workspaceID?: string,
): Record<string, string> | null {
  if (workspaceID === undefined) {
    return headers && Object.keys(headers).length > 0 ? { ...headers } : null;
  }
  return applyWorkspaceRouteHeader(headers, workspaceID);
}
