import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { SlidersHorizontal, X, ArrowRight } from 'lucide-react';
import { api } from '../lib/api';
import { useLoad } from '../lib-use';
import { AssetCard, Modal, StateView, unitLabels } from '../components/shared';
import { CatalogSearch } from '../components/CatalogSearch';
import { CatalogSelect } from '../components/CatalogSelect';
import type { FilterGroup } from '../types';
import '../catalog.css';
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
        <legend>预算范围</legend>
        <CatalogSelect
          label="报价单位"
          value={params.get('price_unit') || ''}
          onChange={(value) => update('price_unit', value)}
          options={[
            { value: '', label: '选择报价单位' },
            ...Object.entries(unitLabels)
              .filter(([key]) => !units || units.includes(key))
              .filter(([key]) =>
                params.get('trade_type') === 'sale'
                  ? key === 'account'
                  : params.get('trade_type') === 'rent'
                    ? key !== 'account'
                    : true,
              )
              .map(([value, label]) => ({ value, label: label.replace('/ ', '每') })),
          ]}
        />
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
        <p className="budget-hint">先选择报价单位，再按预算筛选或价格排序。</p>
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
  const resetFilters = (source: URLSearchParams) => {
    const p = new URLSearchParams(source);
    ['price_unit', 'min_price', 'max_price', 'cursor', ...groups.map((g) => g.key)].forEach((key) =>
      p.delete(key),
    );
    if (p.get('sort')?.startsWith('price')) p.delete('sort');
    return p;
  };
  const active = [...params.entries()].filter(
    ([k]) => !['sort', 'cursor', 'q', 'trade_type'].includes(k),
  );
  const filterLabel = (key: string, value: string) => {
    if (key === 'price_unit') return unitLabels[value]?.replace('/ ', '每') || value;
    if (key === 'min_price') return `最低 ¥${value}`;
    if (key === 'max_price') return `最高 ¥${value}`;
    return (
      groups.find((g) => g.key === key)?.options.find((o) => o.value === value)?.label || value
    );
  };
  const Heading = home ? 'h2' : 'h1';
  return (
    <section
      className={`collection-section catalog-redesign page-width mx-auto ${home ? '' : 'catalog-page'}`}
      id="collection"
    >
      <div className="catalog-masthead">
        <div className="catalog-intro">
          <Heading>{home ? '发现心仪账号' : '账号集市'}</Heading>
          <p>从区服、预算与风格开始，找到属于你的珍藏。</p>
        </div>
        <CatalogSearch
          query={q}
          onChange={setQ}
          onSearch={() => {
            const p = new URLSearchParams(params);
            const query = q.trim();
            query ? p.set('q', query) : p.delete('q');
            setQ(query);
            update(p);
          }}
          onClear={() => {
            setQ('');
            const p = new URLSearchParams(params);
            p.delete('q');
            update(p);
          }}
        />
      </div>
      <div className="category-row">
        <div className="category-tabs" role="group" aria-label="账号分类">
          {[
            ['', '全部账号'],
            ['sale', '账号出售'],
            ['rent', '账号租赁'],
          ].map(([value, label]) => (
            <button
              key={value}
              type="button"
              aria-pressed={(params.get('trade_type') || '') === value}
              className={(params.get('trade_type') || '') === value ? 'active' : ''}
              onClick={() => {
                const p = new URLSearchParams();
                if (value) p.set('trade_type', value);
                if (params.get('q')) p.set('q', params.get('q')!);
                if (params.get('sort') && !params.get('sort')!.startsWith('price')) {
                  p.set('sort', params.get('sort')!);
                }
                update(p);
              }}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      <div className="collection-layout">
        <aside className="filter-sidebar" aria-label="账号筛选">
          <div className="filter-panel">
            <div className="filter-heading">
              <h3>
                <SlidersHorizontal aria-hidden="true" />
                筛选条件
              </h3>
              <button className="text-button" onClick={() => update(resetFilters(params))}>
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
            <div className="result-summary" aria-live="polite">
              <span className="catalog-result-query" title={params.get('q') || undefined}>
                {params.get('q') ? `“${params.get('q')}”` : '全部结果'}
              </span>
              <span className="catalog-result-count">
                <strong>{result.data?.total ?? '—'}</strong> 个账号
              </span>
            </div>
            <div className="toolbar-actions">
              <button
                className="mobile-filter-button"
                onClick={() => setDraft(new URLSearchParams(params))}
              >
                <SlidersHorizontal />
                筛选{active.length > 0 && <b>{active.length}</b>}
              </button>
              <CatalogSelect
                className="catalog-sort"
                label="排序方式"
                prefix
                value={params.get('sort') || 'featured'}
                onChange={sort}
                options={[
                  { value: 'featured', label: '精选推荐' },
                  { value: 'hot', label: '热门推荐' },
                  { value: 'newest', label: '最新上架' },
                  {
                    value: 'price-up',
                    label: '价格从低到高',
                    disabled: !params.get('price_unit'),
                    description: !params.get('price_unit') ? '先选择报价单位' : undefined,
                  },
                  {
                    value: 'price-down',
                    label: '价格从高到低',
                    disabled: !params.get('price_unit'),
                    description: !params.get('price_unit') ? '先选择报价单位' : undefined,
                  },
                ]}
              />
            </div>
          </div>
          {active.length > 0 && (
            <div className="active-chips">
              {active.map(([k, v]) => (
                <button
                  key={k + v}
                  aria-label={`移除筛选：${filterLabel(k, v)}`}
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
                  {filterLabel(k, v)}
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
                  共 {result.data.total} 个账号 · 每页 {home ? 6 : 9} 个
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
        <Modal title="筛选条件" onClose={() => setDraft(null)}>
          {categories.error ? (
            <StateView error={categories.error} onRetry={categories.retry} />
          ) : (
            <FilterForm
              params={draft}
              onChange={setDraft}
              groups={groups}
              units={category?.priceUnits}
            />
          )}
          <div className="catalog-drawer-actions">
            <button className="text-button" onClick={() => setDraft(resetFilters(draft))}>
              重置
            </button>
            <button className="button-outline" onClick={() => setDraft(null)}>
              取消
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
