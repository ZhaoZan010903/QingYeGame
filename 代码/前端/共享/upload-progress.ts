export interface CompletedUploads {
  count: number;
  bytes: number;
}

/** Weight by file size, retaining completed totals after their previews are released. */
export function summarizeUploadProgress(
  completed: CompletedUploads,
  pending: readonly {
    file: { size: number };
    progress: number;
    status: string;
  }[],
) {
  const bytes =
    completed.bytes + pending.reduce((sum, item) => sum + item.file.size, 0);
  const transferred =
    completed.bytes +
    pending.reduce((sum, item) => {
      const percent = Number.isFinite(item.progress)
        ? Math.max(0, Math.min(100, item.progress))
        : 0;
      return sum + (item.file.size * percent) / 100;
    }, 0);
  return {
    total: completed.count + pending.length,
    completed: completed.count,
    percent: bytes
      ? Math.min(
          pending.length ? 99 : 100,
          Math.floor((transferred / bytes) * 100),
        )
      : 0,
    failed: pending.filter((item) => item.status === "failed").length,
    paused: pending.filter((item) => item.status === "paused").length,
    saving: pending.filter((item) => item.status === "saving").length,
  };
}

export function publicationProgress(ready: number, total: number) {
  if (!Number.isFinite(total) || total <= 0 || !Number.isFinite(ready))
    return 0;
  return Math.floor((Math.max(0, Math.min(ready, total)) / total) * 100);
}

export function publicationInterrupted(status: string, interrupted?: boolean) {
  return status === "publishing" && interrupted === true;
}
