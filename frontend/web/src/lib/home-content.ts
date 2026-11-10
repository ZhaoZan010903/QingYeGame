import type { Product } from '../types';

export type HomeSectionSource = 'hot' | 'newest';
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
  icon: 'arrow' | 'plus';
}
export interface HomePresentation {
  editorial: { title: string; description: string; intervalMs: number };
  sections: HomeSectionConfig[];
  shortcuts: HomeShortcut[];
  publishing: { title: string; description: string; action: string };
}

/** Prototype defaults, also used when an API has not supplied its optional presentation field.
 * Inventory, ranking, category filters and contact details always belong to their own APIs.
 */
export const defaultHomePresentation: HomePresentation = {
  editorial: {
    title: '本期甄选',
    description: '每一份热爱，都有独特的模样',
    intervalMs: 7500,
  },
  sections: [
    { source: 'hot', title: '热门推荐', description: '此刻，值得关注的珍藏。', limit: 6 },
    { source: 'newest', title: '最新上架', description: '新近公开，等你发现。', limit: 6 },
  ],
  shortcuts: [
    {
      href: '/accounts?trade_type=sale',
      title: '账号出售',
      description: '找到值得拥有的珍藏',
      icon: 'arrow',
    },
    {
      href: '/accounts?trade_type=rent',
      title: '账号租赁',
      description: '为下一次出场，换个风格',
      icon: 'arrow',
    },
    {
      href: '/accounts?sort=newest',
      title: '最新上架',
      description: '看看刚刚公开的账号',
      icon: 'arrow',
    },
    { href: '/publish', title: '发布账号', description: '自主提交，审核后展示', icon: 'plus' },
  ],
  publishing: {
    title: '让珍藏，遇见下一位知己。',
    description: '自主提交资料，或由客服协助整理上架。',
    action: '发布我的资产',
  },
};

const record = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
const text = (value: unknown, fallback: string, max = 120) =>
  typeof value === 'string' && value.trim() && value.length <= max ? value.trim() : fallback;
const integer = (value: unknown, fallback: number, min: number, max: number) =>
  typeof value === 'number' && Number.isInteger(value) && value >= min && value <= max
    ? value
    : fallback;

/** Allow only existing public entry points; server content cannot create external redirects. */
function shortcutHref(value: unknown): string | undefined {
  if (typeof value !== 'string' || value.length > 240 || /[\\\s#]/.test(value)) return;
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
        if ((section.source !== 'hot' && section.source !== 'newest') || seen.has(section.source))
          return [];
        seen.add(section.source);
        const fallback = defaults.sections.find((item) => item.source === section.source)!;
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
        const title = text(shortcut.title, '', 24);
        if (!href || !title || seenShortcuts.has(href)) return [];
        seenShortcuts.add(href);
        return [
          {
            href,
            title,
            description: text(shortcut.description, '', 60),
            icon: shortcut.icon === 'plus' ? 'plus' : 'arrow',
          },
        ];
      })
    : defaults.shortcuts.map((shortcut) => ({ ...shortcut }));
  return {
    editorial: {
      title: text(editorial.title, defaults.editorial.title, 32),
      description: text(editorial.description, defaults.editorial.description),
      intervalMs: integer(editorial.intervalMs, defaults.editorial.intervalMs, 4000, 30000),
    },
    sections,
    shortcuts,
    publishing: {
      title: text(publishing.title, defaults.publishing.title, 60),
      description: text(publishing.description, defaults.publishing.description),
      action: text(publishing.action, defaults.publishing.action, 24),
    },
  };
}

/** Keep backend order; only remove repeats and cap each configured section. Never pad with fixtures. */
export function selectHomeSections(
  inventory: Record<'featured' | HomeSectionSource, Product[]>,
  sections: HomeSectionConfig[],
) {
  const displayed = new Set(inventory.featured.map((product) => product.id));
  return sections.map((section) => {
    const products: Product[] = [];
    for (const product of inventory[section.source]) {
      if (products.length >= section.limit) break;
      if (displayed.has(product.id)) continue;
      displayed.add(product.id);
      products.push(product);
    }
    return { ...section, products };
  });
}
