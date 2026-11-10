import { useEffect, useMemo, useRef, useSyncExternalStore } from 'react';
import { api } from '../lib/api';
import { GalleryPager } from '../lib/gallery-pager';
import type { Product } from '../types';
export function useProductGallery(product: Product) {
  const pager = useMemo(
    () =>
      new GalleryPager((cursor, version) => api.images(product.id, cursor, version), {
        items: product.images,
        total: product.imageTotal,
        version: product.version,
      }),
    [product.id, product.version],
  );
  const state = useSyncExternalStore(pager.subscribe, pager.getSnapshot, pager.getSnapshot);
  const sentinel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    void pager.load();
  }, [pager]);
  useEffect(() => {
    if (
      !('IntersectionObserver' in window) ||
      !sentinel.current ||
      state.loading ||
      state.error ||
      !state.nextCursor
    )
      return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) void pager.load();
      },
      { rootMargin: '600px 0px' },
    );
    observer.observe(sentinel.current);
    return () => observer.disconnect();
  }, [pager, state.nextCursor, state.loading, state.error]);
  return { ...state, sentinel, load: pager.load, getSnapshot: pager.getSnapshot };
}
