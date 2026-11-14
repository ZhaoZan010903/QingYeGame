import type { Product } from "../types";
import {
  rentalQuoteLabel,
  rentalUnitLabels,
} from "../../../共享/rental-quotes";
const units: Record<string, string> = {
  account: "元",
  hour: "元/小时",
  day: "元/日",
  week: "元/周",
  month: "元/月",
  period: "元/时段",
  unknown: "原站报价，单位待核对",
};
export function Price({ product }: { product: Product }) {
  const period =
    product.unit === "period"
      ? product.rentalQuotes?.find((q) => q.unit === "period")
      : undefined;
  return (
    <span className="price-box">
      <span className="price mono">
        {product.unit === "unknown"
          ? (product.legacyPrice ?? "联系客服")
          : product.price === null
            ? "联系客服"
            : `¥ ${product.price.toLocaleString("zh-CN")}`}
      </span>
      <small className="price-unit">
        {product.unit === "unknown"
          ? product.priceNote || units.unknown
          : product.price === null
            ? "报价咨询"
            : period
              ? `${rentalQuoteLabel(period)} · 元/${rentalUnitLabels[period.billing_unit ?? "period"]}`
              : units[product.unit]}
      </small>
    </span>
  );
}
export function StateView({
  loading,
  error,
  empty,
  onRetry,
}: {
  loading?: boolean;
  error?: unknown;
  empty?: string;
  onRetry?: () => void;
}) {
  return (
    <div className="state-view" role={error ? "alert" : "status"}>
      {loading ? (
        <p>正在读取…</p>
      ) : error ? (
        <>
          <p>
            {error instanceof Error
              ? error.message
              : "服务暂不可用，请稍后重试。"}
          </p>
          {onRetry && (
            <button className="button-outline" onClick={onRetry}>
              重试
            </button>
          )}
        </>
      ) : (
        <p>{empty ?? "暂无内容。"}</p>
      )}
    </div>
  );
}
