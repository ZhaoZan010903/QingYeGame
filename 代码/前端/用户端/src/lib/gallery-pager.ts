import type { ImageItem, ImagePage } from '../types';
import { ApiError } from './query';
export interface GalleryState {
  items: ImageItem[];
  total: number;
  version: number;
  nextCursor: string | null;
  initialized: boolean;
  loading: boolean;
  error?: unknown;
}
type FetchPage = (cursor?: string | null, version?: number) => Promise<ImagePage>;

/** One in-flight request per gallery. Failed pages preserve their cursor and existing images. */
export class GalleryPager {
  private state: GalleryState;
  private listeners = new Set<() => void>();
  private inFlight?: Promise<void>;
  private usedCursors = new Set<string>();
  constructor(
    private fetchPage: FetchPage,
    seed: Pick<GalleryState, 'items' | 'total' | 'version'>,
  ) {
    this.state = { ...seed, initialized: false, loading: false, nextCursor: null };
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private update(patch: Partial<GalleryState>) {
    this.state = { ...this.state, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  load = (reset = false): Promise<void> => {
    if (this.inFlight) return this.inFlight;
    if (!reset && this.state.initialized && !this.state.nextCursor) return Promise.resolve();
    const first = reset || !this.state.initialized;
    const cursor = first ? undefined : this.state.nextCursor;
    const version = reset ? undefined : this.state.version;
    this.update({ loading: true, error: undefined });
    this.inFlight = this.fetchPage(cursor, version)
      .then((page) => {
        if (!reset && page.version !== version)
          throw new ApiError('图集版本已更新，请重新加载。', 409, 'VERSION_CONFLICT');
        if (
          page.nextCursor &&
          (page.nextCursor === cursor || (!first && this.usedCursors.has(page.nextCursor)))
        )
          throw new ApiError('图集分页未继续前进，请重试或联系平台。', 502, 'INVALID_CURSOR');
        const items = first ? [] : [...this.state.items];
        const ids = new Set(items.map((item) => item.id));
        for (const item of page.items)
          if (!ids.has(item.id)) {
            items.push(item);
            ids.add(item.id);
          }
        if (!page.nextCursor && items.length < page.total)
          throw new ApiError('服务返回的图片清单不完整，请重试。', 502, 'INCOMPLETE_GALLERY');
        if (first) this.usedCursors.clear();
        if (cursor) this.usedCursors.add(cursor);
        this.update({
          items,
          total: page.total,
          version: page.version,
          nextCursor: page.nextCursor,
          initialized: true,
        });
      })
      .catch((error) => {
        this.update({ error });
      })
      .finally(() => {
        this.inFlight = undefined;
        this.update({ loading: false });
      });
    return this.inFlight;
  };
}
