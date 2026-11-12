import type { Product, ProductWrite } from '../types';

export function formFromProduct(value: Product): ProductWrite {
  return {
    title: value.title,
    mode: value.mode,
    unit: value.unit,
    price: value.price,
    server: value.server,
    gender: value.gender,
    level: value.level,
    kind: value.kind,
    categoryId: value.categoryId,
    description: value.description,
    attributes: value.attributes,
    tags: Array.isArray(value.attributes?.custom_tags) ? value.attributes.custom_tags : [],
    rentalQuotes: value.rentalQuotes,
  };
}

/** A gallery response can advance an edit version only with a complete form refresh. */
export function galleryFormDecision(current: Product | null, incoming: Product, dirty: boolean) {
  if (dirty && current) return incoming.version === current.version ? 'preserve' : 'conflict';
  return 'replace';
}
