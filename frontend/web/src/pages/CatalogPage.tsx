import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { SlidersHorizontal, X, ArrowRight } from 'lucide-react';
import { api } from '../lib/api';
import { useLoad } from '../lib-use';
import { AssetCard, Modal, StateView, unitLabels } from '../components/shared';
import type { FilterGroup } from '../types';
const defaultFilters: FilterGroup[] = [
  {
    key: 'server',
    label: '游戏区服',
    options: ['华东一区', '华南一区', '华北一区'].map((v) => ({ value: v, label: v })),
  },
  {
    key: 'gender',
    label: '角色性别',
    options: [
      { value: 'female', label: '女角色' },
      { value: 'male', label: '男角色' },
    ],
  },
  {
    key: 'level',
    label: 'XVIP 等级',
    options: [7, 8, 9, 10].map((n) => ({ value: String(n), label: `VIP ${n}` })),
  },
  {
    key: 'kind',
    label: '账号类型',
    options: [
      { value: 'single', label: '独立账号' },
      { value: 'linked', label: '双号连体' },
    ],
  },
];
function FilterForm({
  params,
  onChange,
  groups,
  units,
}: {
  params: URLSearchParams;
  onChange: (p: URLSearchParams) => void;
  groups: FilterGroup[];
  units?: string[];
}) {
  const update = (key: string, value: string) => {
    const p = new URLSearchParams(params);
    value ? p.set(key, value) : p.delete(key);
    p.delete('cursor');
    if (key === 'price_unit' && !value) {
      p.delete('min_price');
      p.delete('max_price');
      if (p.get('sort')?.startsWith('price')) p.delete('sort');
    }
    onChange(p);
  };
  const toggle = (key: string, value: string) => {
    const p = new URLSearchParams(params),
      values = p.getAll(key);
    p.delete(key);
    (values.includes(value) ? values.filter((v) => v !== value) : [...values, value]).forEach((v) =>
      p.append(key, v),
    );
    p.delete('cursor');
    onChange(p);
  };
  return (
    <div className="filter-form">
      <fieldset className="filter-field">
        <legend>交易类型</legend>
        <select
          aria-label="交易类型"
          value={params.get('trade_type') || ''}
          onChange={(e) => {
            const p = new URLSearchParams(params);
            e.target.value ? p.set('trade_type', e.target.value) : p.delete('trade_type');
            p.delete('price_unit');
            p.delete('min_price');
            p.delete('max_price');
            p.delete('cursor');
            if (p.get('sort')?.startsWith('price')) p.delete('sort');
            onChange(p);
          }}
        >
          <option value="">全部账号</option>
          <option value="sale">账号出售</option>
          <option value="rent">账号租赁</option>
        </select>
      </fieldset>
      <fieldset className="filter-field">
        <legend>报价单位</legend>
        <select
          aria-label="报价单位"
          value={params.get('price_unit') || ''}
          onChange={(e) => update('price_unit', e.target.value)}
        >
          <option value="">全部单位</option>
          {Object.entries(unitLabels)
            .filter(([key]) => !units || units.includes(key))
            .filter(([key]) =>
              params.get('trade_type') === 'sale'
                ? key === 'account'
                : params.get('trade_type') === 'rent'
                  ? key !== 'account'
                  : true,
            )
            .map(([key, label]) => (
              <option key={key} value={key}>
                {label.replace('/ ', '每')}
              </option>
            ))}
        </select>
        <div className="price-inputs">
          <input
            type="number"
            min="0"
            placeholder="最低价"
            aria-label="最低价"
            disabled={!params.get('price_unit')}
            value={params.get('min_price') || ''}
            onChange={(e) => update('min_price', e.target.value)}
          />
          <span>—</span>
          <input
            type="number"
            min="0"
            placeholder="最高价"
            aria-label="最高价"
            disabled={!params.get('price_unit')}
            value={params.get('max_price') || ''}
            onChange={(e) => update('max_price', e.target.value)}
          />
        </div>
        <p className="budget-hint">选择同一报价单位后，可比较预算与价格。</p>
      </fieldset>
      {groups.map((g) => (
        <fieldset className="filter-field" key={g.key}>
          <legend>
            {g.label}
            <small>可多选</small>
          </legend>
          <div className="filter-options">
            {g.options.map((o) => (
              <label key={o.value}>
                <input
                  type="checkbox"
                  checked={params.getAll(g.key).includes(o.value)}
                  onChange={() => toggle(g.key, o.value)}
                />
                <span>{o.label}</span>
              </label>
            ))}
          </div>
        </fieldset>
      ))}
    </div>
  );
}
export function Catalog({ home = false }: { home?: boolean }) {
  const [params, setParams] = useSearchParams();
  const [draft, setDraft] = useState<URLSearchParams | null>(null);
  const [q, setQ] = useState(params.get('q') || '');
  useEffect(() => setQ(params.get('q') || ''), [params.get('q')]);
  const categories = useLoad(() => api.categories(), 'categories');
  const category =
    categories.data?.find((c) => c.tradeType === params.get('trade_type')) ||
    categories.data?.find((c) => !c.tradeType) ||
    categories.data?.[0];
  const groups = category?.filters || defaultFilters;
  const request = new URLSearchParams(params);
  request.set('limit', home ? '6' : '9');
  const filterValues: Record<string, string[]> = {};
  groups.forEach((group) => {
    const values = request.getAll(group.key);
    if (values.length) filterValues[group.key] = values;
    request.delete(group.key);
  });
  if (Object.keys(filterValues).length) request.set('filters', JSON.stringify(filterValues));
  const result = useLoad(() => api.products(request), request.toString());
  const update = (p: URLSearchParams) => {
    p.delete('cursor');
    setParams(p, { preventScrollReset: true });
  };
  const sort = (v: string) => {
    const p = new URLSearchParams(params);
    p.set('sort', v);
    update(p);
  };
  const active = [...params.entries()].filter(([k]) => !['sort', 'cursor', 'q'].includes(k));
  return (
    <section
      className={`collection-section page-width mx-auto ${home ? '' : 'catalog-page'}`}
      id="collection"
    >
      <div className="collection-header flex justify-between items-end">
        <div>
          <span className="section-eyebrow mono">EXPLORE THE COLLECTION / 03</span>
          <h2>{home ? '浏览全部账号' : '找到属于你的珍藏'}</h2>
          <p>每一种风格，都有自己的表达。</p>
        </div>
        <span className="collection-status mono">QQ X5 COLLECTION</span>
      </div>
      <div className="category-row">
        <div className="category-tabs" aria-label="账号分类">
          {[
            ['', '全部账号'],
            ['sale', '账号出售'],
            ['rent', '账号租赁'],
          ].map(([value, label]) => (
            <button
              key={value}
              className={(params.get('trade_type') || '') === value ? 'active' : ''}
              onClick={() => {
                const p = new URLSearchParams();
                if (value) p.set('trade_type', value);
                update(p);
              }}
            >
              {label}
            </button>
          ))}
        </div>
        <form
          className="catalog-search"
          onSubmit={(e) => {
            e.preventDefault();
            const p = new URLSearchParams(params);
            q ? p.set('q', q) : p.delete('q');
            update(p);
          }}
        >
          <input
            aria-label="列表搜索"
            placeholder="编号 / 标题 / 区服"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <button type="submit" aria-label="搜索列表">
            <ArrowRight />
          </button>
        </form>
      </div>
      <div className="collection-layout">
        <aside className="filter-sidebar" aria-label="资产筛选">
          <div className="filter-panel">
            <div className="filter-heading">
              <h3>精确筛选</h3>
              <button className="text-button" onClick={() => update(new URLSearchParams())}>
                重置
              </button>
            </div>
            {categories.error ? (
              <StateView error={categories.error} onRetry={categories.retry} />
            ) : (
              <FilterForm
                params={params}
                onChange={update}
                groups={groups}
                units={category?.priceUnits}
              />
            )}
          </div>
        </aside>
        <div className="collection-results">
          <div className="results-toolbar flex items-center justify-between">
            <div className="result-summary">
              <span className="result-dot" />
              <span>{params.get('q') ? `搜索「${params.get('q')}」` : '精选资产'}</span>
              <span className="mono">{result.data?.total ?? '—'}</span>
            </div>
            <div className="toolbar-actions">
              <button
                className="mobile-filter-button"
                onClick={() => setDraft(new URLSearchParams(params))}
              >
                <SlidersHorizontal />
                筛选{active.length > 0 && <b>{active.length}</b>}
              </button>
              <select
                className="sort-select"
                aria-label="排序方式"
                value={params.get('sort') || 'featured'}
                onChange={(e) => sort(e.target.value)}
              >
                <option value="featured">精选推荐</option>
                <option value="hot">热门推荐</option>
                <option value="newest">最新上架</option>
                <option value="price-up" disabled={!params.get('price_unit')}>
                  价格从低到高
                </option>
                <option value="price-down" disabled={!params.get('price_unit')}>
                  价格从高到低
                </option>
              </select>
            </div>
          </div>
          {active.length > 0 && (
            <div className="active-chips">
              {active.map(([k, v]) => (
                <button
                  key={k + v}
                  onClick={() => {
                    const p = new URLSearchParams(params);
                    const values = p.getAll(k).filter((value) => value !== v);
                    p.delete(k);
                    values.forEach((value) => p.append(k, value));
                    if (k === 'price_unit') {
                      p.delete('min_price');
                      p.delete('max_price');
                      if (p.get('sort')?.startsWith('price')) p.delete('sort');
                    }
                    update(p);
                  }}
                >
                  {k === 'price_unit'
                    ? unitLabels[v]
                    : k === 'gender'
                      ? v === 'female'
                        ? '女角色'
                        : '男角色'
                      : k === 'trade_type'
                        ? v === 'sale'
                          ? '出售'
                          : '租赁'
                        : k === 'min_price'
                          ? `最低 ¥${v}`
                          : k === 'max_price'
                            ? `最高 ¥${v}`
                            : v}
                  <X />
                </button>
              ))}
            </div>
          )}
          {result.loading || result.error ? (
            <StateView {...result} onRetry={result.retry} />
          ) : result.data?.items.length ? (
            <>
              <div className="asset-grid">
                {result.data.items.map((p) => (
                  <AssetCard key={p.id} product={p} />
                ))}
              </div>
              <div className="pagination">
                {params.has('cursor') && (
                  <button
                    className="button-outline"
                    onClick={() => {
                      const p = new URLSearchParams(params);
                      p.delete('cursor');
                      setParams(p);
                    }}
                  >
                    返回第一页
                  </button>
                )}
                {result.data.nextCursor && (
                  <button
                    className="button-outline"
                    onClick={() => {
                      const p = new URLSearchParams(params);
                      p.set('cursor', result.data!.nextCursor!);
                      setParams(p);
                    }}
                  >
                    下一页 <ArrowRight />
                  </button>
                )}
                <span>
                  {result.data.total} 项资产 · 每页 {home ? 6 : 9} 项
                </span>
              </div>
            </>
          ) : (
            <>
              <StateView empty="暂未找到符合条件的账号" />
              <button className="button-outline" onClick={() => update(new URLSearchParams())}>
                重置筛选
              </button>
            </>
          )}
        </div>
      </div>
      {draft && (
        <Modal title="精确筛选" onClose={() => setDraft(null)}>
          <FilterForm
            params={draft}
            onChange={setDraft}
            groups={groups}
            units={category?.priceUnits}
          />
          <div className="drawer-actions">
            <button className="button-outline" onClick={() => setDraft(new URLSearchParams())}>
              重置
            </button>
            <button
              className="button-white"
              onClick={() => {
                update(draft);
                setDraft(null);
              }}
            >
              应用筛选 <ArrowRight />
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}
export function CatalogPage() {
  return <Catalog />;
}
