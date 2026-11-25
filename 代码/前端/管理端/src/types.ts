export * from "../../共享/types";
import type { Product as SharedProduct } from "../../共享/types";

export type Product = SharedProduct & {
  isPubliclyVisible?: boolean;
  recommendationBoost?: number;
  recommendationVersion?: number;
};
