/** A level can be a configured text code, not just a numeric VIP tier. */
export function readLevel(value: unknown): number | string {
  if (typeof value === "number") return Number.isFinite(value) ? value : "";
  if (typeof value === "string") return value.trim();
  if (Array.isArray(value))
    return value
      .map(readLevel)
      .filter((item) => item !== "")
      .join("、");
  return "";
}
export function levelLabel(value: number | string): string {
  if (value === "") return "未填写";
  return typeof value === "number" || /^\d+$/.test(value)
    ? `VIP ${value}`
    : value;
}
