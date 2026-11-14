interface EditableRecord {
  status: string;
  version: number;
  publication_interrupted?: boolean;
}
import type { Product } from "../types";

/** Content and recommendation writes have independent versions; keep each confirmed snapshot current. */
export function mergeRecommendationVersion<T extends Record<string, any>>(
  current: Record<string, any> | null,
  incoming: T,
): T {
  if (
    current &&
    current.id === incoming.id &&
    Number(current?.recommendation_version ?? 0) > 0 &&
    Number(current.recommendation_version) >=
      Number(incoming.recommendation_version ?? 0)
  ) {
    return {
      ...incoming,
      recommendation_boost: current.recommendation_boost,
      recommendation_version: current.recommendation_version,
    };
  }
  return incoming;
}

/** Gallery APIs return the complete public edit snapshot, not just image metadata. */
export function editorRecordFromProduct<T extends Record<string, any>>(
  current: T | null,
  product: Product,
) {
  return mergeRecommendationVersion(current, {
    ...current,
    id: product.id,
    title: product.title,
    category_id: product.categoryId,
    trade_type: product.mode,
    price_unit: product.unit,
    price_amount: product.price == null ? "" : String(product.price),
    description: product.description,
    attributes: product.attributes ?? {},
    rental_quotes: product.rentalQuotes ?? [],
    rental_category: product.rentalCategory ?? "",
    recommendation_boost:
      product.recommendationBoost ?? current?.recommendation_boost ?? 0,
    recommendation_version:
      product.recommendationVersion ?? current?.recommendation_version ?? 1,
    status: product.status,
    version: product.version,
    image_total: product.imageTotal,
    images: product.images,
    product_code: product.code,
    source: product.source,
    has_private_credentials: product.hasPrivateCredentials,
    publication_interrupted: product.publicationInterrupted,
  });
}
interface SyncState {
  status: string;
  version: number;
  interrupted?: boolean;
}
export function canAcceptFreshProduct(
  expected: { id: string; version: number },
  current: { id: string; version: number } | null,
  dirty: boolean,
) {
  return (
    !dirty &&
    current?.id === expected.id &&
    current.version === expected.version
  );
}

/** A progress response is not a full editable snapshot: never upgrade the write version from it. */
export function mergeMediaSyncDisplay<T extends EditableRecord>(
  current: T,
  next: SyncState,
  dirty: boolean,
) {
  const requiresReload =
    next.version !== current.version || next.status !== current.status;
  return {
    record: {
      ...current,
      status: next.status,
      publication_interrupted: next.interrupted === true,
    },
    requiresReload,
    message: requiresReload
      ? dirty
        ? "商品状态或资料已更新。未保存的编辑仍保留在此页，请核对后重新加载最新版本再操作。"
        : "商品状态或资料已更新，请重新加载最新版本后继续编辑或操作。"
      : "",
  };
}
