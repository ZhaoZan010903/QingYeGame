const rentalUnits = ["hour", "day", "week", "month"];

/** The API uses the first quote as the card price, so preserve the editor's primary quote. */
export function buildRentalQuotes(
  mode: string,
  primaryUnit: string,
  primaryAmount: string,
  additional: Record<string, string>,
  allowedUnits: string[] = rentalUnits,
) {
  if (mode !== "rent") return [];
  const configured = allowedUnits.length ? allowedUnits : rentalUnits;
  if (primaryUnit !== "unknown" && !configured.includes(primaryUnit))
    throw new Error("当前交易分类不支持主报价单位，请重新选择。");
  const values = Object.entries(additional).filter(
    ([unit]) => unit !== primaryUnit,
  );
  if (primaryAmount.trim() && rentalUnits.includes(primaryUnit))
    values.unshift([primaryUnit, primaryAmount.trim()]);
  return values.map(([unit, amount]) => {
    if (!configured.includes(unit))
      throw new Error("存在当前分类不支持的租期报价，请核对后手动移除。");
    if (!rentalUnits.includes(unit) || !/^\d{1,10}(?:\.\d{1,2})?$/.test(amount))
      throw new Error("已选择的租期需要填写有效金额，最多两位小数。");
    return { unit, amount };
  });
}

/** Legacy tags remain in attributes.tags; only new custom tags follow the current limit. */
export function customTagsForEditing(attributes?: Record<string, unknown>) {
  return Array.isArray(attributes?.custom_tags)
    ? attributes.custom_tags.filter((tag) => typeof tag === "string").join("，")
    : "";
}

export function parseCustomTags(raw: string, previous: unknown = []) {
  const tags = [
    ...new Set(
      raw
        .split(/[,，、\n]/)
        .map((tag) => tag.trim())
        .filter(Boolean),
    ),
  ];
  const original = Array.isArray(previous)
    ? previous.filter((tag): tag is string => typeof tag === "string")
    : [];
  const unchanged =
    tags.length === original.length &&
    tags.every((tag, index) => tag === original[index]);
  // Existing oversized tags may be retained; new edits follow the smaller public label design.
  if (
    (tags.length > 20 && !unchanged) ||
    tags.some((tag) => Array.from(tag).length > 5 && !original.includes(tag))
  )
    throw new Error(
      "最多填写 20 个标签，新标签每个不能超过 5 字。旧标签可保留原文。",
    );
  return tags;
}
