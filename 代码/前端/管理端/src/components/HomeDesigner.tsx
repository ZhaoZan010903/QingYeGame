import { useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowDown,
  ArrowUp,
  ArrowRight,
  Eye,
  Plus,
  Trash2,
} from "lucide-react";
import {
  resolveHomePresentation,
  type HomePresentation,
} from "../lib/home-content";
import { publicSite } from "../lib/admin-ux";

type Zone =
  "editorial" | "hot" | "recommended" | "newest" | "shortcuts" | "publishing";
const zones: { key: Zone; title: string; where: string }[] = [
  { key: "editorial", title: "精选轮播", where: "首页上方 · 主视觉下方" },
  { key: "shortcuts", title: "快捷入口", where: "精选轮播下方 · 分类入口" },
  { key: "hot", title: "热门账号", where: "首页商品区 · 浏览与收藏热度" },
  { key: "recommended", title: "为你推荐", where: "首页商品区 · 根据个人浏览" },
  { key: "newest", title: "最新上架", where: "首页商品区 · 按上架时间" },
  { key: "publishing", title: "发布邀请", where: "首页下方 · 发布账号入口" },
];
const destinations = [
  { href: "/accounts?trade_type=sale", name: "出售商品列表" },
  { href: "/accounts?trade_type=rent", name: "租赁商品列表" },
  { href: "/accounts?sort=newest", name: "最新上架列表" },
  { href: "/accounts", name: "全部商品列表" },
  { href: "/publish", name: "发布账号页面" },
];
function TextField({
  label,
  value,
  onChange,
  max = 120,
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  max?: number;
  hint?: string;
}) {
  return (
    <label className="form-field">
      {label}
      <input
        value={value}
        maxLength={max}
        onChange={(e) => onChange(e.target.value)}
      />
      <small>{hint || `最多 ${max} 字`}</small>
    </label>
  );
}
export function HomeDesigner({
  value,
  onChange,
}: {
  value: HomePresentation;
  onChange: (v: HomePresentation) => void;
}) {
  const [zone, setZone] = useState<Zone>("editorial");
  const current = zones.find((item) => item.key === zone)!;
  const section = value.sections.find((item) => item.source === zone);
  const patchSection = (patch: object) =>
    onChange({
      ...value,
      sections: value.sections.map((item) =>
        item.source === zone ? { ...item, ...patch } : item,
      ),
    });
  const enabled = !!section;
  function toggleSection() {
    const source = zone as "hot" | "recommended" | "newest";
    onChange({
      ...value,
      sections: enabled
        ? value.sections.filter((item) => item.source !== source)
        : [
            ...value.sections,
            resolveHomePresentation().sections.find(
              (item) => item.source === source,
            )!,
          ],
    });
  }
  function moveSection(direction: number) {
    const sections = [...value.sections],
      index = sections.findIndex((item) => item.source === zone),
      next = index + direction;
    if (index < 0 || next < 0 || next >= sections.length) return;
    [sections[index], sections[next]] = [sections[next], sections[index]];
    onChange({ ...value, sections });
  }
  return (
    <div className="home-designer">
      <aside className="home-map">
        <div className="section-heading">
          <strong>选择修改区域</strong>
          <span>
            <Eye size={14} /> 位置示意
          </span>
        </div>
        <div className="home-wireframe">
          <div className="wireframe-top">
            <span>清叶</span>
            <span>首页　账号集市　发布</span>
          </div>
          <div className="wireframe-hero">
            <small>固定品牌主视觉</small>
            <strong>珍藏你的热爱。</strong>
          </div>
          <button
            type="button"
            className={`wire-zone ${zone === "editorial" ? "selected" : ""}`}
            onClick={() => setZone("editorial")}
          >
            <span className="zone-tag">精选轮播</span>
            <strong>{value.editorial.title}</strong>
            <small>{value.editorial.description}</small>
            <div className="wire-carousel">
              <span>精选商品 · 截图位置</span>
              <i>01 / 03</i>
            </div>
          </button>
          <button
            type="button"
            className={`wire-zone ${zone === "shortcuts" ? "selected" : ""}`}
            onClick={() => setZone("shortcuts")}
          >
            <span className="zone-tag">快捷入口</span>
            <div className="wire-shortcuts">
              {value.shortcuts.length ? (
                value.shortcuts.map((item, i) => (
                  <span key={i}>{item.title}</span>
                ))
              ) : (
                <small>当前已隐藏</small>
              )}
            </div>
          </button>
          {value.sections.map((item) => (
            <button
              type="button"
              className={`wire-zone ${zone === item.source ? "selected" : ""}`}
              key={item.source}
              onClick={() => setZone(item.source)}
            >
              <span className="zone-tag">
                {
                  {
                    hot: "热门账号",
                    recommended: "为你推荐",
                    newest: "最新上架",
                  }[item.source]
                }
              </span>
              <strong>{item.title}</strong>
              <small>{item.description}</small>
              <div className="wire-products">
                {[1, 2, 3].map((n) => (
                  <span key={n}>商品 {n}</span>
                ))}
              </div>
              <small>最多显示 {item.limit} 件 · 以可用商品为准</small>
            </button>
          ))}
          <button
            type="button"
            className={`wire-zone ${zone === "publishing" ? "selected" : ""}`}
            onClick={() => setZone("publishing")}
          >
            <span className="zone-tag">发布邀请</span>
            <strong>{value.publishing.title}</strong>
            <small>{value.publishing.description}</small>
            <span className="wire-cta">{value.publishing.action} →</span>
          </button>
        </div>
        <p className="field-hint">
          这是区域示意，不是用户站完整还原。轮播与推荐商品来自实际数据。
        </p>
        {publicSite && (
          <a
            className="text-button"
            href={publicSite}
            target="_blank"
            rel="noreferrer"
          >
            查看实际首页 ↗
          </a>
        )}
      </aside>
      <div className="home-fields">
        <div className="zone-tabs">
          {zones.map((item) => (
            <button
              type="button"
              key={item.key}
              className={zone === item.key ? "active" : ""}
              onClick={() => setZone(item.key)}
            >
              {item.title}
            </button>
          ))}
        </div>
        <div className="location-note">
          <strong>正在修改 · {current.title}</strong>
          <span>{current.where}</span>
        </div>
        {zone === "editorial" && (
          <>
            <TextField
              label="轮播区标题"
              max={32}
              value={value.editorial.title}
              onChange={(title) =>
                onChange({ ...value, editorial: { ...value.editorial, title } })
              }
            />
            <TextField
              label="轮播区说明"
              value={value.editorial.description}
              onChange={(description) =>
                onChange({
                  ...value,
                  editorial: { ...value.editorial, description },
                })
              }
            />
            <label className="form-field">
              每张轮播停留时间（秒）
              <input
                type="number"
                min={4}
                max={30}
                step={0.5}
                required
                value={value.editorial.intervalMs / 1000}
                onChange={(e) =>
                  onChange({
                    ...value,
                    editorial: {
                      ...value.editorial,
                      intervalMs: Number(e.target.value) * 1000,
                    },
                  })
                }
              />
              <small>建议 7.5 秒，允许 4–30 秒。</small>
            </label>
            <div className="inline-help">
              <strong>想更换轮播里的商品？</strong>
              <p>
                进入商品管理，选择已上架商品，在“检查并上架”中填写首页精选顺序。
              </p>
              <Link to="/products?status=published">
                去选择商品 <ArrowRight size={14} />
              </Link>
            </div>
          </>
        )}
        {(zone === "hot" || zone === "newest" || zone === "recommended") && (
          <>
            <label className="switch-row">
              <span>
                <strong>在首页显示这个区域</strong>
                <small>关闭后只隐藏这个区块，不会下架商品。</small>
              </span>
              <input
                type="checkbox"
                checked={enabled}
                onChange={toggleSection}
              />
            </label>
            {section && (
              <>
                <TextField
                  label="区域标题"
                  max={32}
                  value={section.title}
                  onChange={(title) => patchSection({ title })}
                />
                <TextField
                  label="区域说明"
                  value={section.description}
                  onChange={(description) => patchSection({ description })}
                />
                <label className="form-field">
                  最多展示多少件商品
                  <input
                    required
                    type="number"
                    min={1}
                    max={24}
                    step={1}
                    value={section.limit}
                    onChange={(e) =>
                      patchSection({ limit: Number(e.target.value) })
                    }
                  />
                  <small>
                    1–24 件。与轮播等区块重复的商品会去重，实际可能少于此数量。
                  </small>
                </label>
                <div className="reorder-controls">
                  <span>商品区顺序</span>
                  <button
                    className="button-outline"
                    type="button"
                    disabled={value.sections[0]?.source === zone}
                    onClick={() => moveSection(-1)}
                  >
                    <ArrowUp size={14} />
                    向前
                  </button>
                  <button
                    className="button-outline"
                    type="button"
                    disabled={value.sections.at(-1)?.source === zone}
                    onClick={() => moveSection(1)}
                  >
                    <ArrowDown size={14} />
                    向后
                  </button>
                </div>
                {zone === "hot" && (
                  <div className="inline-help">
                    <p>
                      这里管理显示内容。想了解排序或更新榜单，请到热门榜单。
                    </p>
                    <Link to="/recommendations">管理热门榜单 →</Link>
                  </div>
                )}
                {zone === "recommended" && (
                  <div className="inline-help">
                    <p>
                      系统根据每位用户的近期浏览选择账号；这里仅设置标题、数量和位置。没有浏览记录或关闭个性化推荐的用户会看到热门账号。
                    </p>
                  </div>
                )}
              </>
            )}
          </>
        )}
        {zone === "shortcuts" && (
          <>
            <p className="field-hint">
              最多 4 个入口。用户点击后跳转到你选择的页面。
            </p>
            {value.shortcuts.map((item, index) => (
              <div className="subform-card" key={index}>
                <div className="section-heading">
                  <strong>入口 {index + 1}</strong>
                  <button
                    type="button"
                    className="text-button danger-text"
                    aria-label={`删除入口 ${index + 1}`}
                    onClick={() =>
                      onChange({
                        ...value,
                        shortcuts: value.shortcuts.filter(
                          (_, i) => i !== index,
                        ),
                      })
                    }
                  >
                    <Trash2 size={14} />
                    删除
                  </button>
                </div>
                <TextField
                  label="入口名称"
                  max={24}
                  value={item.title}
                  onChange={(title) =>
                    onChange({
                      ...value,
                      shortcuts: value.shortcuts.map((s, i) =>
                        i === index ? { ...s, title } : s,
                      ),
                    })
                  }
                />
                <TextField
                  label="一句话说明"
                  max={60}
                  value={item.description}
                  onChange={(description) =>
                    onChange({
                      ...value,
                      shortcuts: value.shortcuts.map((s, i) =>
                        i === index ? { ...s, description } : s,
                      ),
                    })
                  }
                />
                <label className="form-field">
                  点击后打开
                  <select
                    value={item.href}
                    onChange={(e) =>
                      onChange({
                        ...value,
                        shortcuts: value.shortcuts.map((s, i) =>
                          i === index
                            ? {
                                ...s,
                                href: e.target.value,
                                icon:
                                  e.target.value === "/publish"
                                    ? "plus"
                                    : "arrow",
                              }
                            : s,
                        ),
                      })
                    }
                  >
                    {!destinations.some((d) => d.href === item.href) && (
                      <option value={item.href}>现有筛选页面（保留）</option>
                    )}
                    {destinations.map((d) => (
                      <option
                        value={d.href}
                        key={d.href}
                        disabled={value.shortcuts.some(
                          (s, i) => i !== index && s.href === d.href,
                        )}
                      >
                        {d.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            ))}
            <button
              type="button"
              className="button-outline"
              disabled={value.shortcuts.length >= 4}
              onClick={() => {
                const target = destinations.find(
                  (d) => !value.shortcuts.some((s) => s.href === d.href),
                )!;
                onChange({
                  ...value,
                  shortcuts: [
                    ...value.shortcuts,
                    {
                      href: target.href,
                      title: target.name,
                      description: "",
                      icon: target.href === "/publish" ? "plus" : "arrow",
                    },
                  ],
                });
              }}
            >
              <Plus size={16} />
              添加快捷入口
            </button>
          </>
        )}
        {zone === "publishing" && (
          <>
            <TextField
              label="邀请标题"
              max={60}
              value={value.publishing.title}
              onChange={(title) =>
                onChange({
                  ...value,
                  publishing: { ...value.publishing, title },
                })
              }
            />
            <TextField
              label="邀请说明"
              value={value.publishing.description}
              onChange={(description) =>
                onChange({
                  ...value,
                  publishing: { ...value.publishing, description },
                })
              }
            />
            <TextField
              label="发布按钮文字"
              max={24}
              hint="点击后打开发布账号页面。"
              value={value.publishing.action}
              onChange={(action) =>
                onChange({
                  ...value,
                  publishing: { ...value.publishing, action },
                })
              }
            />
          </>
        )}
      </div>
    </div>
  );
}
