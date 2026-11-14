import { ApiError } from "./query";

export interface TrashPreview {
  snapshot: string;
  total: number;
  image_count: number;
}
export interface PurgeResult {
  purged_count: number;
  cleanup_pending: true;
}
interface PurgeInput {
  reason: string;
  request_id: string;
}
export interface SinglePurgeInput extends PurgeInput {
  expected_version: number;
  confirmation: "永久删除";
}
export interface TrashPurgeInput extends PurgeInput {
  snapshot: string;
  confirmation: "清空回收站";
}
const count = (value: unknown) =>
  Number.isSafeInteger(value) && Number(value) >= 0;
export function readTrashPreview(value: unknown): TrashPreview {
  const data = value as Partial<TrashPreview> | null;
  if (
    !data ||
    typeof data.snapshot !== "string" ||
    !/^[a-f0-9]{64}$/.test(data.snapshot) ||
    !count(data.total) ||
    !count(data.image_count)
  ) {
    throw new Error("回收站预览数据不完整，请重新预览。");
  }
  return data as TrashPreview;
}
export function readPurgeResult(value: unknown): PurgeResult {
  const data = value as Partial<PurgeResult> | null;
  if (!data || !count(data.purged_count) || data.cleanup_pending !== true) {
    // A successful HTTP response with an unreadable result may already have committed.
    throw new ApiError(
      "删除结果暂未确认，请重试本次操作。",
      0,
      "INVALID_PURGE_RESPONSE",
    );
  }
  return data as PurgeResult;
}
export function purgeFailureKind(
  error: unknown,
): "refresh" | "unknown" | "rejected" {
  if (!(error instanceof ApiError)) return "unknown";
  if (error.status === 409 || error.status === 404 || error.status === 410)
    return "refresh";
  if (
    error.status === 0 ||
    error.status === 408 ||
    error.status >= 500 ||
    error.code === "INVALID_RESPONSE"
  )
    return "unknown";
  return "rejected";
}
export function trashPreviewLimit(preview: TrashPreview): string {
  if (preview.total > 1000 || preview.image_count > 100000) {
    return "一次最多清空 1,000 个商品、100,000 张图片。请先单条删除部分商品，再重新预览。";
  }
  return "";
}
