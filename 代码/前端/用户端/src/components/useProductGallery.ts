import { useEffect, useMemo, useSyncExternalStore } from 'react';
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
  useEffect(() => {
    void pager.load();
  }, [pager]);
  return { ...state, load: pager.load, getSnapshot: pager.getSnapshot };
}
