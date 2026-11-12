import type { ImagePage, Product } from "./types";

export interface ImageRecoveryTransport {
  myProduct(id: string): Promise<Product>;
  myImages(
    id: string,
    cursor?: string | null,
    version?: number,
  ): Promise<ImagePage>;
  removeImage(id: string, imageId: string, version: number): Promise<Product>;
}

/** A lost append response is ambiguous: read the versioned gallery before retrying. */
export async function locateProductImage(
  transport: ImageRecoveryTransport,
  productId: string,
  imageId: string,
  check: () => void,
) {
  check();
  const product = await transport.myProduct(productId);
  check();
  if (product.images.some((image) => image.id === imageId))
    return { product, attached: true };
  const seen = new Set<string>();
  let cursor: string | null = null;
  do {
    const page = await transport.myImages(productId, cursor, product.version);
    check();
    if (page.version !== product.version)
      throw new Error("图集已更新，请重试或重新加载。");
    if (page.items.some((image) => image.id === imageId))
      return { product, attached: true };
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor))
      throw new Error("图集分页异常，请重新加载后再试。");
    if (cursor) seen.add(cursor);
  } while (cursor);
  return { product, attached: false };
}

/** Remove the intended image only; never discard a failed queue entry before confirmation. */
export async function removePendingProductImage(
  transport: ImageRecoveryTransport,
  productId: string,
  imageId: string,
  cancel: () => Promise<void>,
  check: () => void,
): Promise<Product> {
  let located = await locateProductImage(transport, productId, imageId, check);
  if (!located.attached) {
    try {
      check();
      await cancel();
      check();
      return located.product;
    } catch (error) {
      if (!(
        error &&
        typeof error === "object" &&
        "status" in error &&
        error.status === 409
      ))
        throw error;
      // Append may have committed between the read and cancel. Reconcile once, never loop blindly.
      located = await locateProductImage(transport, productId, imageId, check);
      if (!located.attached) throw error;
    }
  }
  check();
  const next = await transport.removeImage(
    productId,
    imageId,
    located.product.version,
  );
  check();
  return next;
}
