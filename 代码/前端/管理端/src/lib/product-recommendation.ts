import { request } from "./api";

export interface RecommendationSnapshot {
  id: string;
  boost: number;
  version: number;
}

export function parseRecommendationBoost(value: string): number {
  if (!/^(?:0|[1-9]\d?|100)$/.test(value.trim()))
    throw new Error("推荐加分需填写 0–100 的整数。");
  return Number(value.trim());
}

export function readRecommendation(
  value: Record<string, unknown>,
): RecommendationSnapshot {
  const boost = value.recommendation_boost;
  const version = value.recommendation_version;
  if (
    typeof value.id !== "string" ||
    !Number.isInteger(boost) ||
    Number(boost) < 0 ||
    Number(boost) > 100 ||
    !Number.isSafeInteger(version) ||
    Number(version) < 1
  )
    throw new Error("推荐设置响应不完整，请重新读取当前设置。");
  return { id: value.id, boost: Number(boost), version: Number(version) };
}

export async function saveRecommendation(
  id: string,
  boost: number,
  version: number,
) {
  const result = await request(
    `/admin/products/${encodeURIComponent(id)}/recommendation`,
    "PATCH",
    {
      boost,
      expected_version: version,
    },
  );
  return readRecommendation(result.data ?? result);
}
