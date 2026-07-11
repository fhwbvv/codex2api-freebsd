import type { BatchUpdateAccountsRequest } from "../types";

export interface BuildBatchMetadataUpdateOptions {
  ids: number[];
  updateTags: boolean;
  tags: string[];
  updateGroups: boolean;
  groupIds: number[];
}

export function buildBatchMetadataUpdate({
  ids,
  updateTags,
  tags,
  updateGroups,
  groupIds,
}: BuildBatchMetadataUpdateOptions): BatchUpdateAccountsRequest {
  const payload: BatchUpdateAccountsRequest = { ids: [...ids] };
  if (updateTags) payload.tags = [...tags];
  if (updateGroups) payload.group_ids = [...groupIds];
  return payload;
}
