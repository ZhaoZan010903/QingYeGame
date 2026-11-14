export * from "../../共享/types";
import type { Product as SharedProduct } from "../../共享/types";

export type Product = SharedProduct & {
  recommendationBoost?: number;
  recommendationVersion?: number;
};
