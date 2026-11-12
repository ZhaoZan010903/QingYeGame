import type { HomePresentation } from "./home-content";

export type TradeType = "sale" | "rent";
export type PriceUnit =
  "account" | "hour" | "day" | "week" | "month" | "unknown";
export type ProductStatus =
  | "draft"
  | "pending"
  | "publishing"
  | "published"
  | "rejected"
  | "rejected_final"
  | "offline"
  | "sold"
  | "rented";
export type VerificationStatus = "unprovided" | "pending" | "verified";
export interface RentalQuote {
  unit: "hour" | "day" | "week" | "month";
  amount: string;
}
export interface ImageItem {
  id: string;
  url?: string;
  thumbnailUrl?: string;
  art?: number;
  alt: string;
}
export interface Product {
  attributeLabels?: Partial<
    Record<"server" | "gender" | "level" | "kind", string>
  >;
  draftExpiresAt?: string | null;
  rentalQuotes?: RentalQuote[];
  hasPrivateCredentials?: boolean;
  consultationCount?: number;
  id: string;
  code?: string;
  categoryId?: string;
  source?: string;
  legacyPrice?: string;
  legacyCategory?: string;
  priceNote?: string;
  attributes?: Record<string, unknown>;
  title: string;
  mode: TradeType;
  unit: PriceUnit;
  price: number | null;
  server: string;
  gender: "female" | "male" | "unknown";
  level: number | string;
  kind: "single" | "linked";
  tags: string[];
  description: string;
  art: number;
  images: ImageItem[];
  imageTotal: number;
  status: ProductStatus;
  publicationInterrupted?: boolean;
  version: number;
  verification: VerificationStatus;
  rare: boolean;
  publishedAt: string;
  createdAt?: string;
  updatedAt?: string;
  deletedAt?: string | null;
  reviewNote?: string;
  favorite?: boolean;
  editorial?: {
    kicker: string;
    name: string;
    collection: string;
    description: string;
  };
}
export interface ProductWrite {
  rentalQuotes?: RentalQuote[];
  privateCredentials?: { qqAccount: string; qqPassword: string };
  attributes?: Record<string, unknown>;
  title: string;
  mode: TradeType;
  unit: PriceUnit;
  price: number | null;
  server: string;
  gender: "female" | "male" | "unknown";
  level: number | string;
  tags: string[];
  description: string;
  kind?: "single" | "linked";
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
  recommended?: Product[];
  personalizationSource?: "browsing" | "cold-start" | "disabled";
  newest: Product[];
  ruleVersion?: string;
  computedAt?: string;
}
export interface User {
  id: string;
  nickname: string;
  phone: string;
  demo?: boolean;
  role?: "member" | "admin";
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
  required?: boolean;
  placeholder?: string;
  enabled?: boolean;
  key: string;
  label: string;
  options: FilterOption[];
  multiple?: boolean;
  type?: string;
}
export interface Category {
  id: string;
  name: string;
  tradeType?: TradeType;
  priceUnits: PriceUnit[];
  filters: FilterGroup[];
}
