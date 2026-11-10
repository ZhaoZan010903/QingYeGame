import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, isDemo } from '../lib/api';
import type { Category, PriceUnit, Product, ProductWrite } from '../types';
import { useUi } from '../context';
import { ImageUploader } from '../components/ImageUploader';
import '../member.css';

const initial: ProductWrite = {
  title: '',
  mode: 'sale',
  unit: 'account',
  price: null,
  server: '',
  gender: 'female',
  level: 0,
  kind: 'single',
  tags: [],
  description: '',
};
const unitNames: Record<PriceUnit, string> = {
  account: '出售总价',
  hour: '每小时',
  day: '每日',
  week: '每周',
  month: '每月',
};
const errorText = (e: unknown) =>
  e instanceof Error ? e.message : '保存失败，已填写的内容仍然保留，请重试。';
export function PublishPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { notify, contact } = useUi();
  const [form, setForm] = useState<ProductWrite>(initial);
  const [price, setPrice] = useState('');
  const [tags, setTags] = useState('');
  const [product, setProduct] = useState<Product | null>(null);
  const latest = useRef<Product | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [uploadBusy, setUploadBusy] = useState(false);
  const [pendingImages, setPendingImages] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [reload, setReload] = useState(0);
  const saving = useRef<Promise<Product> | null>(null);
  const skipLeave = useRef(false);
  const updateProduct = useCallback((next: Product) => {
    latest.current = next;
    setProduct(next);
  }, []);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setLoadError('');
    Promise.all([api.categories(), id ? api.myProduct(id) : Promise.resolve(null)])
      .then(([list, value]) => {
        if (!active) return;
        setCategories(list);
        latest.current = value;
        setProduct(value);
        const source: ProductWrite = value
          ? {
              title: value.title,
              mode: value.mode,
              unit: value.unit,
              price: value.price,
              server: value.server,
              gender: value.gender,
              level: value.level,
              kind: value.kind,
              tags: value.tags,
              description: value.description,
            }
          : { ...initial };
        source.categoryId = list.find((c) => !c.tradeType || c.tradeType === source.mode)?.id;
        setForm(source);
        setPrice(source.price == null ? '' : String(source.price));
        setTags(source.tags.join('、'));
        setDirty(false);
      })
      .catch((e) => {
        if (active) setLoadError(errorText(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [id, reload]);
  useEffect(() => {
    if (!dirty && !uploadBusy && !pendingImages) return;
    const unload = (e: BeforeUnloadEvent) => {
      if (!skipLeave.current) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    const click = (e: MouseEvent) => {
      if (
        skipLeave.current ||
        e.defaultPrevented ||
        e.button !== 0 ||
        e.metaKey ||
        e.ctrlKey ||
        e.shiftKey ||
        e.altKey
      )
        return;
      const link = (e.target as HTMLElement).closest('a');
      if (
        !link ||
        link.target === '_blank' ||
        link.hasAttribute('download') ||
        link.href === window.location.href ||
        link.getAttribute('href')?.startsWith('#')
      )
        return;
      if (
        !window.confirm(
          uploadBusy
            ? '图片正在处理，离开会取消未完成的任务。确认离开？'
            : '还有未保存的内容或待处理图片，确认离开？',
        )
      ) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    window.addEventListener('beforeunload', unload);
    document.addEventListener('click', click, true);
    return () => {
      window.removeEventListener('beforeunload', unload);
      document.removeEventListener('click', click, true);
    };
  }, [dirty, uploadBusy, pendingImages]);
  const change = <K extends keyof ProductWrite>(key: K, value: ProductWrite[K]) => {
    setForm((previous) => ({ ...previous, [key]: value }));
    setDirty(true);
  };
  function input(): ProductWrite {
    if (price && (!/^\d+(?:\.\d{1,2})?$/.test(price) || !Number.isFinite(Number(price))))
      throw new Error('参考报价应为非负金额，最多保留两位小数。');
    return {
      ...form,
      title: form.title.trim(),
      description: form.description.trim(),
      price: price.trim() ? Number(price) : null,
      tags: tags
        .split(/[,，、\n]/)
        .map((t) => t.trim())
        .filter(Boolean),
    };
  }
  async function persist(): Promise<Product> {
    if (saving.current) return saving.current;
    const data = input();
    if (!data.categoryId) throw new Error('尚未配置可发布分类，请联系客服。');
    const p = latest.current;
    const operation = api.saveProduct(data, p?.id, p?.version);
    saving.current = operation;
    try {
      const next = await operation;
      updateProduct(next);
      setDirty(false);
      return next;
    } finally {
      saving.current = null;
    }
  }
  async function save(submit = false) {
    if (uploadBusy || busy) return;
    setError('');
    try {
      if (submit) {
        const data = input();
        if (!data.title || !data.server || !data.description)
          throw new Error('提交审核前，请填写账号标题、区服和账号介绍。');
        if (pendingImages)
          throw new Error('还有未完成或失败的图片。请完成重试或移除待处理项后提交。');
        if (!latest.current?.imageTotal) throw new Error('请至少添加一张已保存的账号图片。');
      }
      setBusy(true);
      const saved = await persist();
      if (submit) {
        updateProduct(await api.submitProduct(saved.id, saved.version));
        notify(
          isDemo
            ? '演示记录已进入审核中；没有提交至真实后台。'
            : '资料已提交审核，可在我的发布查看结果。',
        );
        skipLeave.current = true;
        navigate('/me/listings');
      } else {
        notify(isDemo ? '草稿已保存在当前设备。' : '草稿已保存。');
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  function submitForm(e: FormEvent) {
    e.preventDefault();
    void save(true);
  }
  const category = categories.find((c) => c.id === form.categoryId);
  const serverOptions = category?.filters.find((group) => group.key === 'server')?.options || [];
  const allUnits = category?.priceUnits || [];
  const units =
    form.mode === 'sale'
      ? (['account'] as PriceUnit[])
      : allUnits.filter((unit) => unit !== 'account');
  const locked = product?.status === 'published' || product?.status === 'pending';
  return (
    <section className="publish-page page-width mx-auto" aria-label="发布账号">
      <Link to="/me/listings" className="publish-back">
        ← 我的发布
      </Link>
      <div className="publish-heading">
        <span className="section-eyebrow mono">YOUR NEXT CHAPTER</span>
        <h1>{id || product ? '整理你的账号资料' : '让珍藏，开启下一程。'}</h1>
        <p>
          填写资料并整理图片，保存草稿或提交平台审核。也可以
          <button className="text-button" onClick={() => contact(product?.id, 'publish')}>
            联系客服协助上架 ↗
          </button>
        </p>
      </div>
      {loading ? (
        <div className="member-loading" role="status">
          正在读取发布配置…
        </div>
      ) : loadError ? (
        <div className="member-error">
          <p role="alert" className="form-error">
            {loadError}
          </p>
          <button className="button-outline" onClick={() => setReload((v) => v + 1)}>
            重新加载
          </button>
        </div>
      ) : locked ? (
        <section className="member-empty">
          <span className="empty-symbol">◇</span>
          <h3>{product.status === 'pending' ? '这份资料正在审核中' : '这份账号已公开展示'}</h3>
          <p>
            {product.status === 'pending'
              ? '请先在我的发布撤回审核，再继续修改。'
              : '已展示商品的复审方式尚未配置，请联系平台客服协助修改。'}
          </p>
          <Link to="/me/listings" className="button-white">
            返回我的发布
          </Link>
        </section>
      ) : (
        <>
          {isDemo && (
            <p className="publish-hint">
              当前为前端演示。草稿、图片和审核状态仅保存在此设备，不会公开上架或发送至真实业务后台。
            </p>
          )}
          {product?.reviewNote && (
            <p className="publish-hint">最近审核意见：{product.reviewNote}</p>
          )}
          <div className="publish-steps" aria-label="发布流程">
            <span>
              <b>01</b>填写账号资料
            </span>
            <i />
            <span>
              <b>02</b>整理展示图片
            </span>
            <i />
            <span>
              <b>03</b>提交平台审核
            </span>
          </div>
          <form onSubmit={submitForm} noValidate>
            <fieldset disabled={busy || uploadBusy} style={{ border: 0, padding: 0, minWidth: 0 }}>
              <section className="publish-panel">
                <h2>01 · 账号资料</h2>
                <p>公开展示的信息请如实填写，不要提交账号密码、验证码或身份材料。</p>
                <div className="form-grid">
                  <div className="form-field span-2">
                    <span>发布类型</span>
                    <div className="mode-options">
                      {(['sale', 'rent'] as const).map((mode) => (
                        <label
                          className={`mode-option ${form.mode === mode ? 'selected' : ''}`}
                          key={mode}
                        >
                          <input
                            type="radio"
                            name="tradeType"
                            value={mode}
                            checked={form.mode === mode}
                            onChange={() => {
                              const next = categories.find(
                                (c) => !c.tradeType || c.tradeType === mode,
                              );
                              setForm((previous) => ({
                                ...previous,
                                mode,
                                categoryId: next?.id,
                                unit:
                                  mode === 'sale'
                                    ? 'account'
                                    : next?.priceUnits.find((u) => u !== 'account') || 'day',
                              }));
                              setDirty(true);
                            }}
                          />
                          {mode === 'sale' ? '账号出售' : '账号租赁'}
                        </label>
                      ))}
                    </div>
                  </div>
                  <label className="form-field span-2">
                    账号标题
                    <input
                      value={form.title}
                      onChange={(e) => change('title', e.target.value)}
                      maxLength={100}
                      placeholder="例如：月白 · 典藏衣橱 / 华东一区"
                    />
                    <small>准确描述账号特色，最多 100 字。</small>
                  </label>
                  <label className="form-field">
                    游戏分类
                    <select
                      value={form.categoryId || ''}
                      onChange={(e) => {
                        change('categoryId', e.target.value);
                      }}
                    >
                      <option value="" disabled>
                        请选择分类
                      </option>
                      {categories
                        .filter((c) => !c.tradeType || c.tradeType === form.mode)
                        .map((c) => (
                          <option key={c.id} value={c.id}>
                            {c.name}
                          </option>
                        ))}
                    </select>
                  </label>
                  <label className="form-field">
                    所在区服
                    {serverOptions.length ? (
                      <select
                        value={form.server}
                        onChange={(e) => change('server', e.target.value)}
                      >
                        <option value="">请选择区服</option>
                        {serverOptions.map((server) => (
                          <option key={server.value} value={server.value}>
                            {server.label}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <input
                        value={form.server}
                        onChange={(e) => change('server', e.target.value)}
                        maxLength={40}
                        placeholder="填写游戏区服"
                      />
                    )}
                  </label>
                  <label className="form-field">
                    角色性别
                    <select
                      value={form.gender}
                      onChange={(e) => change('gender', e.target.value as Product['gender'])}
                    >
                      <option value="female">女角色</option>
                      <option value="male">男角色</option>
                    </select>
                  </label>
                  <label className="form-field">
                    VIP / XVIP 等级
                    <input
                      type="number"
                      min={0}
                      max={99}
                      step={1}
                      value={form.level}
                      onChange={(e) =>
                        change(
                          'level',
                          Math.max(0, Math.min(99, Math.floor(Number(e.target.value) || 0))),
                        )
                      }
                    />
                  </label>
                  <label className="form-field">
                    账号类型
                    <select
                      value={form.kind || 'single'}
                      onChange={(e) => change('kind', e.target.value as Product['kind'])}
                    >
                      <option value="single">单号独立</option>
                      <option value="linked">双号连体</option>
                    </select>
                  </label>
                  <label className="form-field">
                    参考报价单位
                    <select
                      value={form.unit}
                      onChange={(e) => change('unit', e.target.value as PriceUnit)}
                    >
                      {units.map((unit) => (
                        <option key={unit} value={unit}>
                          {unitNames[unit]}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="form-field">
                    参考报价（人民币）
                    <input
                      inputMode="decimal"
                      value={price}
                      onChange={(e) => {
                        setPrice(e.target.value);
                        setDirty(true);
                      }}
                      placeholder="留空表示联系客服报价"
                      maxLength={14}
                    />
                    <small>不同报价单位分别展示和比较。</small>
                  </label>
                  <label className="form-field">
                    账号特点
                    <input
                      value={tags}
                      onChange={(e) => {
                        setTags(e.target.value);
                        setDirty(true);
                      }}
                      maxLength={200}
                      placeholder="例如：典藏 ×12、稀有翅膀"
                    />
                    <small>用顿号或逗号分隔，不填写未经确认的保障承诺。</small>
                  </label>
                  <label className="form-field span-2">
                    账号介绍
                    <textarea
                      value={form.description}
                      onChange={(e) => change('description', e.target.value)}
                      maxLength={10000}
                      placeholder="介绍服饰、典藏、账号特点和需要说明的情况。请勿填写密码、身份证件或私人联系方式。"
                    />
                    <small>{form.description.length} / 10000</small>
                  </label>
                </div>
              </section>
            </fieldset>
            <section className="publish-panel">
              <h2>02 · 展示图片</h2>
              <p>添加图片时会先保存当前草稿。图片可以持续追加、调整顺序与移除。</p>
              <ImageUploader
                product={product}
                ensureDraft={persist}
                onChange={updateProduct}
                onBusyChange={setUploadBusy}
                onPendingChange={setPendingImages}
                disabled={busy}
              />
            </section>
            <p className="profile-note" style={{ margin: '20px 0' }}>
              提交审核后，商品需要经过平台审核才能公开展示。发布审核与账号资料核验分别处理，审核通过不代表验号或包赔承诺。
            </p>
            {error && (
              <p className="form-error" role="alert">
                {error}
              </p>
            )}
            <div className="publish-actions">
              <p>
                {uploadBusy ? (
                  '图片正在处理，请稍候'
                ) : product ? (
                  <>
                    <span className="mono">{product.id}</span> ·{' '}
                    {dirty ? '有未保存修改' : '草稿已保存'}
                  </>
                ) : (
                  '资料可以先保存，之后再继续整理。'
                )}
              </p>
              <div>
                <button
                  type="button"
                  className="button-outline"
                  onClick={() => void save()}
                  disabled={busy || uploadBusy}
                >
                  {busy ? '正在保存…' : '保存草稿'}
                </button>
                <button type="submit" className="button-white" disabled={busy || uploadBusy}>
                  {busy ? '正在处理…' : '提交审核'} <span aria-hidden="true">↗</span>
                </button>
              </div>
            </div>
          </form>
        </>
      )}
    </section>
  );
}
