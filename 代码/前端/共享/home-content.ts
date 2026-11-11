import type { Product } from "./types";

export type HomeSectionSource = "hot" | "recommended" | "newest";
export interface HomeSlide {
  productId: string;
  title: string;
  description: string;
  tradeLabel: string;
  serverLabel: string;
  priceLabel: string;
  imageId?: string;
  imagePositionX: number;
  imagePositionY: number;
  imageFit: "cover" | "contain";
  layout: "image-right" | "image-left" | "overlay";
  textAlign: "left" | "center" | "right";
  verticalAlign: "top" | "center" | "bottom";
  placement?: "flow" | "free";
  textPositionX?: number;
  textPositionY?: number;
  textWidth?: number;
  titleSize?: number;
  descriptionSize?: number;
  fontFamily?: "sans" | "serif";
  titleColor?: string;
  descriptionColor?: string;
}
export interface HomeSectionConfig {
  source: HomeSectionSource;
  title: string;
  description: string;
  limit: number;
}
export interface HomeShortcut {
  href: string;
  title: string;
  description: string;
  icon: "arrow" | "plus";
}
export interface HomePresentation {
  maxSlides?: number;
  slides?: HomeSlide[];
  editorial: { title: string; description: string; intervalMs: number };
  sections: HomeSectionConfig[];
  shortcuts: HomeShortcut[];
  publishing: { title: string; description: string; action: string };
}

/** Prototype defaults, also used when an API has not supplied its optional presentation field.
 * Inventory, ranking, category filters and contact details always belong to their own APIs.
 */
export const defaultHomePresentation: HomePresentation = {
  maxSlides: 12,
  slides: [],
  editorial: {
    title: "本期甄选",
    description: "每一份热爱，都有独特的模样",
    intervalMs: 7500,
  },
  sections: [
    {
      source: "hot",
      title: "热门账号",
      description: "大家正在浏览和收藏的账号。",
      limit: 6,
    },
    {
      source: "recommended",
      title: "为你推荐",
      description: "根据你近期浏览的账号整理。",
      limit: 6,
    },
    {
      source: "newest",
      title: "最新上架",
      description: "新近公开，等你发现。",
      limit: 6,
    },
  ],
  shortcuts: [
    {
      href: "/accounts?trade_type=sale",
      title: "账号出售",
      description: "找到值得拥有的珍藏",
      icon: "arrow",
    },
    {
      href: "/accounts?trade_type=rent",
      title: "账号租赁",
      description: "为下一次出场，换个风格",
      icon: "arrow",
    },
    {
      href: "/accounts?sort=newest",
      title: "最新上架",
      description: "看看刚刚公开的账号",
      icon: "arrow",
    },
    {
      href: "/publish",
      title: "发布账号",
      description: "自主提交，审核后展示",
      icon: "plus",
    },
  ],
  publishing: {
    title: "让珍藏，遇见下一位知己。",
    description: "自主提交资料，或由客服协助整理上架。",
    action: "发布我的资产",
  },
};

const record = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
const text = (value: unknown, fallback: string, max = 120) =>
  typeof value === "string" && value.trim() && Array.from(value).length <= max
    ? value.trim()
    : fallback;
const integer = (value: unknown, fallback: number, min: number, max: number) =>
  typeof value === "number" &&
  Number.isInteger(value) &&
  value >= min &&
  value <= max
    ? value
    : fallback;

/** Allow only existing public entry points; server content cannot create external redirects. */
function shortcutHref(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length > 240 || /[\\\s#]/.test(value))
    return;
  if (/^\/(accounts(?:\?[^?]*)?|publish)$/.test(value)) return value;
}

/** Validate optional /home presentation content at the boundary, never in React components.
 * Missing or non-array groups use defaults. An explicit [] deliberately hides that group.
 * Section order is configurable; unsupported and duplicate sources are discarded.
 */
export function resolveHomePresentation(value?: unknown): HomePresentation {
  const input = record(value);
  const editorial = record(input.editorial);
  const publishing = record(input.publishing);
  const defaults = defaultHomePresentation;
  const seen = new Set<HomeSectionSource>();
  const sections = Array.isArray(input.sections)
    ? input.sections.flatMap((entry): HomeSectionConfig[] => {
        const section = record(entry);
        if (
          (section.source !== "hot" &&
            section.source !== "newest" &&
            section.source !== "recommended") ||
          seen.has(section.source)
        )
          return [];
        seen.add(section.source);
        const fallback = defaults.sections.find(
          (item) => item.source === section.source,
        )!;
        return [
          {
            source: section.source,
            title: text(section.title, fallback.title, 32),
            description: text(section.description, fallback.description),
            limit: integer(section.limit, fallback.limit, 1, 24),
          },
        ];
      })
    : defaults.sections.map((section) => ({ ...section }));
  const seenShortcuts = new Set<string>();
  const shortcuts = Array.isArray(input.shortcuts)
    ? input.shortcuts.slice(0, 4).flatMap((entry): HomeShortcut[] => {
        const shortcut = record(entry);
        const href = shortcutHref(shortcut.href);
        const title = text(shortcut.title, "", 24);
        if (!href || !title || seenShortcuts.has(href)) return [];
        seenShortcuts.add(href);
        return [
          {
            href,
            title,
            description: text(shortcut.description, "", 60),
            icon: shortcut.icon === "plus" ? "plus" : "arrow",
          },
        ];
      })
    : defaults.shortcuts.map((shortcut) => ({ ...shortcut }));
  return {
    maxSlides: integer(input.maxSlides, 12, 1, 30),
    slides: Array.isArray(input.slides)
      ? input.slides.slice(0, integer(input.maxSlides, 12, 1, 30)).flatMap((entry): HomeSlide[] => {
          const slide = record(entry);
          if (
            typeof slide.productId !== "string" ||
            !slide.productId ||
            typeof slide.title !== "string" ||
            !slide.title.trim()
          )
            return [];
          return [
            {
              productId: slide.productId,
              title: text(slide.title, "", 100),
              description: text(slide.description, "", 300),
              tradeLabel: text(slide.tradeLabel, "", 24),
              serverLabel: text(slide.serverLabel, "", 60),
              priceLabel: text(slide.priceLabel, "", 60),
              imageId:
                typeof slide.imageId === "string" ? slide.imageId : undefined,
              imagePositionX: integer(slide.imagePositionX, 50, 0, 100),
              imagePositionY: integer(slide.imagePositionY, 50, 0, 100),
              imageFit: slide.imageFit === "cover" ? "cover" : "contain",
              placement: slide.placement === "free" && slide.layout === "overlay" ? "free" : "flow",
              textPositionX: integer(slide.textPositionX, 0, 0, 100),
              textPositionY: integer(slide.textPositionY, 50, 0, 100),
              textWidth: integer(slide.textWidth, 55, 25, 85),
              titleSize: integer(slide.titleSize, 40, 24, 72),
              descriptionSize: integer(slide.descriptionSize, 16, 12, 24),
              fontFamily: slide.fontFamily === "serif" ? "serif" : "sans",
              titleColor: typeof slide.titleColor === "string" && /^#[0-9a-f]{6}$/i.test(slide.titleColor) ? slide.titleColor : "",
              descriptionColor: typeof slide.descriptionColor === "string" && /^#[0-9a-f]{6}$/i.test(slide.descriptionColor) ? slide.descriptionColor : "",
              layout:
                slide.layout === "overlay" || slide.layout === "image-left"
                  ? slide.layout
                  : "image-right",
              textAlign:
                slide.textAlign === "center" || slide.textAlign === "right"
                  ? slide.textAlign
                  : "left",
              verticalAlign:
                slide.verticalAlign === "top" ||
                slide.verticalAlign === "bottom"
                  ? slide.verticalAlign
                  : "center",
            },
          ];
        })
      : [],
    editorial: {
      title: text(editorial.title, defaults.editorial.title, 32),
      description: text(editorial.description, defaults.editorial.description),
      intervalMs: integer(
        editorial.intervalMs,
        defaults.editorial.intervalMs,
        4000,
        30000,
      ),
    },
    sections,
    shortcuts,
    publishing: {
      title: text(publishing.title, defaults.publishing.title, 60),
      description: text(
        publishing.description,
        defaults.publishing.description,
      ),
      action: text(publishing.action, defaults.publishing.action, 24),
    },
  };
}

/** Optional fields may use defaults; explicitly invalid API content must surface an error. */
export function readHomePresentation(value?: unknown): HomePresentation {
  if (value === undefined || value === null) return resolveHomePresentation();
  const invalid = () => {
    throw new Error("首页展示配置格式有误，请管理员检查首页配置。");
  };
  if (typeof value !== "object" || Array.isArray(value)) return invalid();
  const input = value as Record<string, unknown>;
  if (input.maxSlides !== undefined && (typeof input.maxSlides !== "number" || !Number.isInteger(input.maxSlides) || input.maxSlides < 1 || input.maxSlides > 30)) return invalid();
  if (input.slides !== undefined) {
    if (!Array.isArray(input.slides) || input.slides.length > (input.maxSlides as number ?? 12))
      return invalid();
    const seen = new Set<string>();
    for (const entry of input.slides) {
      const s = record(entry);
      if (
        typeof s.productId !== "string" ||
        !s.productId ||
        seen.has(s.productId) ||
        typeof s.title !== "string" ||
        !s.title.trim()
      )
        return invalid();
      seen.add(s.productId);
      for (const [key, max] of Object.entries({
        title: 100,
        description: 300,
        tradeLabel: 24,
        serverLabel: 60,
        priceLabel: 60,
      })) {
        if (
          s[key] !== undefined &&
          (typeof s[key] !== "string" || Array.from(s[key] as string).length > max)
        )
          return invalid();
      }
      for (const key of ["imagePositionX", "imagePositionY", "textPositionX", "textPositionY"])
        if (
          s[key] !== undefined &&
          (typeof s[key] !== "number" ||
            !Number.isInteger(s[key]) ||
            (s[key] as number) < 0 ||
            (s[key] as number) > 100)
        )
          return invalid();
      for (const [key, [min, max]] of Object.entries({ textWidth: [25, 85], titleSize: [24, 72], descriptionSize: [12, 24] }))
        if (s[key] !== undefined && (typeof s[key] !== "number" || !Number.isInteger(s[key]) || (s[key] as number) < min || (s[key] as number) > max)) return invalid();
      for (const key of ["titleColor", "descriptionColor"])
        if (s[key] !== undefined && s[key] !== "" && (typeof s[key] !== "string" || !/^#[0-9a-f]{6}$/i.test(s[key] as string))) return invalid();
      if (s.placement === "free" && s.layout !== "overlay") return invalid();
      for (const [key, allowed] of Object.entries({
        placement: ["flow", "free"],
        fontFamily: ["sans", "serif"],
        imageFit: ["cover", "contain"],
        layout: ["image-right", "image-left", "overlay"],
        textAlign: ["left", "center", "right"],
        verticalAlign: ["top", "center", "bottom"],
      }))
        if (s[key] !== undefined && !allowed.includes(String(s[key])))
          return invalid();
      if (s.imageId !== undefined && typeof s.imageId !== "string")
        return invalid();
    }
  }
  const checkText = (
    item: Record<string, unknown>,
    key: string,
    max: number,
    allowEmpty = false,
  ) => {
    if (
      item[key] !== undefined &&
      (typeof item[key] !== "string" ||
        (!allowEmpty && !(item[key] as string).trim()) ||
        Array.from(item[key] as string).length > max)
    )
      invalid();
  };
  for (const [key, fields] of Object.entries({
    editorial: { title: 32, description: 120 },
    publishing: { title: 60, description: 120, action: 24 },
  })) {
    if (input[key] === undefined) continue;
    const group = input[key];
    if (!group || typeof group !== "object" || Array.isArray(group))
      return invalid();
    for (const [field, max] of Object.entries(fields))
      checkText(
        group as Record<string, unknown>,
        field,
        max,
        field === "description",
      );
  }
  const interval = record(input.editorial).intervalMs;
  if (
    interval !== undefined &&
    (typeof interval !== "number" ||
      !Number.isInteger(interval) ||
      interval < 4000 ||
      interval > 30000)
  )
    return invalid();
  if (input.sections !== undefined) {
    if (!Array.isArray(input.sections)) return invalid();
    const seen = new Set();
    for (const entry of input.sections) {
      const section = record(entry);
      if (
        !["hot", "newest", "recommended"].includes(String(section.source)) ||
        seen.has(section.source)
      )
        return invalid();
      seen.add(section.source);
      checkText(section, "title", 32);
      checkText(section, "description", 120, true);
      if (
        section.limit !== undefined &&
        (typeof section.limit !== "number" ||
          !Number.isInteger(section.limit) ||
          section.limit < 1 ||
          section.limit > 24)
      )
        return invalid();
    }
  }
  if (input.shortcuts !== undefined) {
    if (!Array.isArray(input.shortcuts) || input.shortcuts.length > 4)
      return invalid();
    const seen = new Set();
    for (const entry of input.shortcuts) {
      const shortcut = record(entry);
      if (
        !shortcutHref(shortcut.href) ||
        seen.has(shortcut.href) ||
        !shortcut.title
      )
        return invalid();
      seen.add(shortcut.href);
      checkText(shortcut, "title", 24);
      checkText(shortcut, "description", 60, true);
      if (
        shortcut.icon !== undefined &&
        shortcut.icon !== "arrow" &&
        shortcut.icon !== "plus"
      )
        return invalid();
    }
  }
  return resolveHomePresentation(value);
}

/** Rank each section independently: latest listings must not disappear because they are also popular. */
export function selectHomeSections(
  inventory: Record<"featured" | "hot" | "newest", Product[]> & {
    recommended?: Product[];
  },
  sections: HomeSectionConfig[],
) {
  return sections.map((section) => {
    const displayed = new Set<string>();
    const products: Product[] = [];
    for (const product of inventory[section.source] ?? []) {
      if (products.length >= section.limit) break;
      if (displayed.has(product.id)) continue;
      displayed.add(product.id);
      products.push(product);
    }
    return { ...section, products };
  });
}
