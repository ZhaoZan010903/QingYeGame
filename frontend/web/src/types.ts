import type { HomePresentation } from './lib/home-content';

export type TradeType = 'sale' | 'rent';
export type PriceUnit = 'account' | 'hour' | 'day' | 'week' | 'month';
export type ProductStatus = 'draft' | 'pending' | 'published' | 'rejected' | 'offline';
export type VerificationStatus = 'unprovided' | 'pending' | 'verified';
export interface ImageItem {
  id: string;
  url?: string;
  art?: number;
  alt: string;
}
export interface Product {
  id: string;
  code?: string;
  title: string;
  mode: TradeType;
  unit: PriceUnit;
  price: number | null;
  server: string;
  gender: 'female' | 'male';
  level: number;
  kind: 'single' | 'linked';
  tags: string[];
  description: string;
  art: number;
  images: ImageItem[];
  imageTotal: number;
  status: ProductStatus;
  version: number;
  verification: VerificationStatus;
  rare: boolean;
  publishedAt: string;
  reviewNote?: string;
  favorite?: boolean;
  editorial?: { kicker: string; name: string; collection: string; description: string };
}
export interface ProductWrite {
  title: string;
  mode: TradeType;
  unit: PriceUnit;
  price: number | null;
  server: string;
  gender: 'female' | 'male';
  level: number;
  tags: string[];
  description: string;
  kind?: 'single' | 'linked';
  categoryId?: string;
}
export interface ProductPage {
  items: Product[];
  total: number;
  nextCursor: string | null;
}
export interface ImagePage {
  items: ImageItem[];
  total: number;
  nextCursor: string | null;
  version: number;
}
export interface HomeData {
  presentation?: HomePresentation;
  featured: Product[];
  hot: Product[];
  newest: Product[];
  ruleVersion?: string;
  computedAt?: string;
}
export interface User {
  id: string;
  nickname: string;
  phone: string;
  demo?: boolean;
}
export interface Contact {
  id: string;
  name: string;
  channel: string;
  value: string;
  qrUrl?: string;
  purpose: string[];
  hours?: string;
}
export interface ContentPage {
  slug: string;
  title: string;
  body: string;
  published: boolean;
  version?: string;
}
export interface FilterOption {
  value: string;
  label: string;
}
export interface FilterGroup {
  key: string;
  label: string;
  options: FilterOption[];
}
export interface Category {
  id: string;
  name: string;
  tradeType?: TradeType;
  priceUnits: PriceUnit[];
  filters: FilterGroup[];
}
