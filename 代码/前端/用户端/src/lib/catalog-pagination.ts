/** Public API cursors contain a raw URL-safe base64 decimal offset. */
export function catalogCursorOffset(cursor: string | null): number {
  if (!cursor) return 0;
  try {
    const value = atob(cursor.replace(/-/g, '+').replace(/_/g, '/'));
    const offset = Number(value);
    return /^\d+$/.test(value) && Number.isSafeInteger(offset) && offset <= 1_000_000 ? offset : 0;
  } catch {
    return 0;
  }
}

export function catalogCursor(offset: number): string {
  return btoa(String(offset)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
}

export function catalogPageNumbers(total: number, cursor: string | null, limit: number) {
  return {
    current: Math.floor(catalogCursorOffset(cursor) / limit) + 1,
    pages: Math.max(1, Math.ceil(total / limit)),
  };
}
