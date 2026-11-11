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
    throw new Error("当前发布模板不支持主报价单位，请重新选择。");
  const values = Object.entries(additional).filter(
    ([unit]) => unit !== primaryUnit,
  );
  if (primaryAmount.trim() && rentalUnits.includes(primaryUnit))
    values.unshift([primaryUnit, primaryAmount.trim()]);
  return values.map(([unit, amount]) => {
    if (!configured.includes(unit))
      throw new Error("存在当前模板不支持的租期报价，请核对后手动移除。");
    if (!rentalUnits.includes(unit) || !/^\d{1,10}(?:\.\d{1,2})?$/.test(amount))
      throw new Error("已选择的租期需要填写有效金额，最多两位小数。");
    return { unit, amount };
  });
}

/** Legacy tags remain in attributes.tags; only the editable field has the 12-tag limit. */
export function customTagsForEditing(attributes?: Record<string, unknown>) {
  return Array.isArray(attributes?.custom_tags)
    ? attributes.custom_tags.filter((tag) => typeof tag === "string").join("，")
    : "";
}

export function parseCustomTags(raw: string) {
  const tags = [
    ...new Set(
      raw
        .split(/[,，、\n]/)
        .map((tag) => tag.trim())
        .filter(Boolean),
    ),
  ];
  if (tags.length > 12 || tags.some((tag) => Array.from(tag).length > 24))
    throw new Error("最多填写 12 个标签，每个标签不能超过 24 字。");
  return tags;
}
