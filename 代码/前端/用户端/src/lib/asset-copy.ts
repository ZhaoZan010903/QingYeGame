/** Presentation only. Never rewrite the persisted title, description or official price. */
export function assetParagraphs(text: string): string[] {
  return text
    .split(/\r?\n+/)
    .flatMap((line) => (line.length > 90 ? line.split(/(?<=[。；;])\s*/) : [line]))
    .map((s) => s.trim())
    .filter(Boolean);
}
export function assetTokens(text: string): string[] | null {
  // Only turn obvious short item lists into chips. Prices and prose stay verbatim.
  if (/[¥￥]|押金|日租|周租|月租|包夜/.test(text)) return null;
  const items = text
    .split(/[，,、]|\s+/)
    .map((item) => item.trim())
    .filter(Boolean);
  return items.length >= 6 && items.every((item) => item.length >= 2 && item.length <= 24)
    ? items
    : null;
}
export function quoteTerms(text: string) {
  const terms = new Map<string, string | null>();
  const pattern =
    /(押金|白时|晚时|日租|包夜|周租|月租)[：:\s]*[¥￥]?\s*(\d{1,7}(?:\.\d{1,2})?)(?![\d.])/g;
  for (const match of text.matchAll(pattern)) {
    const label = match[1],
      value = match[2];
    const tail = text.slice(match.index! + match[0].length);
    if (!/^(?:元|块)?(?:\s|[，,;；。！）)]|$)/.test(tail)) continue;
    // Conflicting amounts in the seller's copy are not presented as a single quote.
    if (!terms.has(label)) terms.set(label, value);
    else if (terms.get(label) !== value) terms.set(label, null);
  }
  return [...terms].flatMap(([label, value]) => (value === null ? [] : [{ label, value }]));
}
