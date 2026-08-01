export function selectAutoScrollKey(
  open: boolean,
  positionReady: boolean,
  value: string,
): string | null {
  if (!open || !positionReady) return null;
  return value;
}
