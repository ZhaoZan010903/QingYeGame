import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, isDemo, request } from '../lib/api';
import type { Category, PriceUnit, Product, ProductWrite } from '../types';
import { useUi } from '../context';
import { ImageUploader } from '../components/ImageUploader';
import '../member.css';
import '../service-refinement.css';
import { PublishPreview } from '../components/PublishPreview';
import {
  canDeleteDraft,
  customPublishTags,
  fixedPublishFields,
  publishAttributeLabel,
  type PublishDictionary,
} from '../lib/publish-dictionaries';
import type { FilterGroup, RentalQuote } from '../types';
import { missingPublishField, publishAttributes, rentalPriceForWrite } from '../lib/publish-fields';
import { formFromProduct, galleryFormDecision } from '../lib/product-form-snapshot';

const initial: ProductWrite = {
  title: '',
  mode: 'sale',
  unit: 'account',
  price: null,
  server: '',
  gender: 'female',
  level: '',
  tags: [],
  description: '',
};
const unitNames: Record<PriceUnit, string> = {
  account: '元（出售总价）',
  hour: '元 / 小时',
  day: '元 / 日',
  week: '元 / 周',
  month: '元 / 月',
  unknown: '报价单位待核对',
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
  const [tagsEdited, setTagsEdited] = useState(false);
  const [dictionaries, setDictionaries] = useState<PublishDictionary[]>([]);
  const [qqAccount, setQQAccount] = useState(''),
    [qqPassword, setQQPassword] = useState('');
  const [rentalPrices, setRentalPrices] = useState<Partial<Record<RentalQuote['unit'], string>>>(
    {},
  );
  const [preview, setPreview] = useState(false);
  const [product, setProduct] = useState<Product | null>(null);
  const latest = useRef<Product | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [uploadBusy, setUploadBusy] = useState(false);
  const [pendingImages, setPendingImages] = useState(false);
  const [dirty, setDirty] = useState(false);
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const [conflict, setConflict] = useState(false);
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [reload, setReload] = useState(0);
  const saving = useRef<Promise<Product> | null>(null);
  const skipLeave = useRef(false);
  const updateProduct = useCallback((next: Product) => {
    latest.current = next;
    setProduct(next);
    const snapshot = formFromProduct(next);
    setForm(snapshot);
    setPrice(next.price == null ? '' : String(next.price));
    setTags(snapshot.tags.join('、'));
    setTagsEdited(false);
    setRentalPrices(
      Object.fromEntries(
        (next.rentalQuotes?.length
          ? next.rentalQuotes
          : next.mode === 'rent' &&
              next.price != null &&
              ['hour', 'day', 'week', 'month'].includes(next.unit)
            ? [{ unit: next.unit, amount: String(next.price) }]
            : []
        ).map((q) => [q.unit, q.amount]),
      ),
    );
    setQQAccount('');
    setQQPassword('');
    dirtyRef.current = false;
    setDirty(false);
  }, []);
  const updateGallery = useCallback(
    (next: Product) => {
      const decision = galleryFormDecision(latest.current, next, dirtyRef.current);
      if (decision === 'conflict') {
        setConflict(true);
        const message = '商品已在其他操作中更新。未保存内容仍保留，请重新加载最新版本后再编辑。';
        setError(message);
        throw new Error(message);
      }
      if (decision === 'replace') updateProduct(next);
      else setProduct(next);
    },
    [updateProduct],
  );
  useEffect(() => {
    let active = true;
    setLoading(true);
    setLoadError('');
    Promise.all([
      api.categories(),
      id ? api.myProduct(id) : Promise.resolve(null),
      isDemo
        ? Promise.resolve([])
        : request('/dictionaries')
            .then((r) => r.data as PublishDictionary[])
            .catch((e) => {
              if (e.status === 404) return [];
              throw e;
            }),
    ])
      .then(([list, value, config]) => {
        if (!active) return;
        setCategories(list);
        setDictionaries(config);
        setQQAccount('');
        setQQPassword('');
        setRentalPrices(
          Object.fromEntries(
            (value?.rentalQuotes?.length
              ? value.rentalQuotes
              : value?.mode === 'rent' &&
                  value.price !== null &&
                  ['hour', 'day', 'week', 'month'].includes(value.unit)
                ? [{ unit: value.unit, amount: String(value.price) }]
                : []
            ).map((q) => [q.unit, q.amount]),
          ),
        );
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
              tags: Array.isArray(value.attributes?.custom_tags)
                ? value.attributes.custom_tags
                : [],
              description: value.description,
              attributes: value.attributes,
            }
          : { ...initial };
        source.categoryId =
          value?.categoryId ?? list.find((c) => !c.tradeType || c.tradeType === source.mode)?.id;
        setForm(source);
        setPrice(source.price == null ? '' : String(source.price));
        setTags(source.tags.join('、'));
        setTagsEdited(false);
        dirtyRef.current = false;
        setDirty(false);
        setConflict(false);
        setError('');
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
    setForm((previous) => {
      const attributes = { ...previous.attributes };
      if (['server', 'gender', 'level', 'kind'].includes(key)) delete attributes[key];
      return { ...previous, attributes, [key]: value };
    });
    setDirty(true);
  };
  function input(): ProductWrite {
    if (price && (!/^\d+(?:\.\d{1,2})?$/.test(price) || !Number.isFinite(Number(price))))
      throw new Error('参考报价应为非负金额，最多保留两位小数。');
    const attributes = publishAttributes(form, []);
    for (const group of groups) attributes[group.key] = fieldValue(group.key);
    const originalTags = latest.current?.attributes?.custom_tags;
    const customTags = customPublishTags(
      tags,
      !tagsEdited && Array.isArray(originalTags) ? originalTags : undefined,
    );
    if (isDemo && (qqAccount || qqPassword))
      throw new Error('演示模式不保存私密账号资料，请使用测试环境。');
    if ((qqAccount && !qqPassword) || (!qqAccount && qqPassword))
      throw new Error('QQ账号和密码请同时填写；不修改时两项都留空。');
    const rentalQuotes: RentalQuote[] =
      form.mode === 'rent'
        ? Object.entries(rentalPrices).map(([unit, amount]) => {
            if (!rentUnits.includes(unit as RentalQuote['unit']))
              throw new Error('交易模板已停用部分租期，请移除停用报价后重新选择。');
            if (!amount || !/^\d+(?:\.\d{1,2})?$/.test(amount) || !Number.isFinite(Number(amount)))
              throw new Error('已选择的租赁单位需要填写有效报价，最多两位小数。');
            return { unit: unit as RentalQuote['unit'], amount: Number(amount).toFixed(2) };
          })
        : [];
    return {
      ...form,
      attributes: { ...attributes, custom_tags: customTags },
      title: form.title.trim(),
      description: form.description.trim(),
      ...(form.mode === 'rent'
        ? rentalPriceForWrite(rentalQuotes, form.unit, rentUnits)
        : { price: price.trim() ? Number(price) : null, unit: form.unit }),
      rentalQuotes,
      ...(qqAccount && qqPassword
        ? { privateCredentials: { qqAccount: qqAccount.trim(), qqPassword } }
        : {}),
      tags: customTags,
    };
  }
  async function persist(): Promise<Product> {
    if (conflict) throw new Error('请先重新加载最新版本，再保存商品。');
    if (saving.current) return saving.current;
    const data = input();
    if (!data.categoryId) throw new Error('尚未配置可发布分类，请联系客服。');
    const p = latest.current;
    const operation = api.saveProduct(data, p?.id, p?.version);
    saving.current = operation;
    try {
      const next = await operation;
      updateProduct(next);
      const savedTags = Array.isArray(next.attributes?.custom_tags)
        ? next.attributes.custom_tags
        : data.tags;
      setTags(savedTags.join('、'));
      setTagsEdited(false);
      setQQAccount('');
      setQQPassword('');
      setDirty(false);
      return next;
    } finally {
      saving.current = null;
    }
  }
  async function save(submit = false) {
    if (uploadBusy || busy || conflict) return;
    setError('');
    try {
      if (submit) {
        const data = input();
        if (!data.title || !data.description)
          throw new Error('提交审核前，请填写账号标题和账号介绍。');
        const missing = missingPublishField(data, groups);
        if (missing) throw new Error('请填写' + missing.label + '。');
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
  async function deleteDraft() {
    if (
      !product ||
      busy ||
      uploadBusy ||
      !window.confirm('删除这份草稿及其已关联图片？删除后无法继续编辑。')
    )
      return;
    setBusy(true);
    setError('');
    try {
      await api.deleteDraft(product.id, product.version);
      skipLeave.current = true;
      notify('草稿已删除。');
      navigate('/me/listings');
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  const category = categories.find((c) => c.id === form.categoryId);
  const groups = fixedPublishFields(dictionaries, categories);
  const fieldValue = (key: string): unknown =>
    form.attributes?.[key] ??
    (
      { server: form.server, gender: form.gender, level: form.level, kind: form.kind } as Record<
        string,
        unknown
      >
    )[key] ??
    '';
  const setField = (group: FilterGroup, value: unknown) => {
    if (['server', 'gender', 'level', 'kind'].includes(group.key))
      change(group.key as 'server', value as string);
    else change('attributes', { ...form.attributes, [group.key]: value });
  };
  const rentUnits = (['hour', 'day', 'week', 'month'] as const).filter(
    (unit) => !category?.priceUnits.length || category.priceUnits.includes(unit),
  );
  const locked =
    product?.status === 'publishing' ||
    product?.status === 'rejected_final' ||
    (isDemo && (product?.status === 'published' || product?.status === 'pending'));
  return (
    <section className="publish-page page-width mx-auto" aria-label="发布账号">
      <Link to="/me/listings" className="publish-back">
        ← 我的发布
      </Link>
      <div className="publish-heading">
        <h1>{id || product ? '整理你的账号资料' : '让珍藏，开启下一程。'}</h1>
        <p>
          填写资料并整理图片，保存草稿或提交平台审核。也可以
          <button
            className="text-button"
            onClick={() => contact(product?.code ?? product?.id, 'publish')}
          >
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
          <h3>
            {product.status === 'publishing'
              ? '商品正在上架'
              : product.status === 'rejected_final'
                ? '这份资料未通过审核'
                : product.status === 'pending'
                  ? '这份资料正在审核中'
                  : '这份账号已公开展示'}
          </h3>
          <p>
            {product.status === 'publishing'
              ? '上架完成后即可公开展示；如需修改，请先在我的发布中下架。'
              : product.status === 'rejected_final'
                ? '请查看审核意见，或联系客服了解原因。'
                : product.status === 'pending'
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
          {!isDemo && (product?.status === 'published' || product?.status === 'pending') && (
            <p className="publish-hint">
              保存资料或修改图片后，商品会转为草稿并停止公开；完成编辑后请重新提交审核。
            </p>
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
            <fieldset
              disabled={busy || uploadBusy || conflict}
              style={{ border: 0, padding: 0, minWidth: 0 }}
            >
              <section className="publish-panel">
                <h2>01 · 账号资料</h2>
                {product?.code && <p className="mono">{product.code}</p>}
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
                            checked={form.mode === mode}
                            disabled={!!product}
                            onChange={() => {
                              const next = categories.find(
                                (c) => !c.tradeType || c.tradeType === mode,
                              );
                              setForm((old) => ({
                                ...old,
                                mode,
                                categoryId: next?.id,
                                unit: mode === 'sale' ? 'account' : 'day',
                                attributes: {},
                              }));
                              setPrice('');
                              setDirty(true);
                            }}
                          />
                          <span>{mode === 'sale' ? '账号出售' : '账号租赁'}</span>
                        </label>
                      ))}
                    </div>
                    {product && <small>租售类型已固定，如需更换请新建资产。</small>}
                  </div>
                  <label className="form-field span-2">
                    账号标题
                    <input
                      value={form.title}
                      onChange={(e) => change('title', e.target.value)}
                      maxLength={1000}
                      placeholder="填写账号标题"
                    />
                  </label>
                  {groups.map((group) => {
                    const raw = fieldValue(group.key);
                    const value = Array.isArray(raw) ? String(raw[0] ?? '') : String(raw);
                    const historical =
                      value && !group.options.some((option) => option.value === value);
                    return (
                      <label className="form-field" key={group.key}>
                        <span>
                          {group.label}
                          {group.required ? ' *' : ''}
                        </span>
                        <select
                          aria-label={group.label}
                          value={value}
                          onChange={(e) => setField(group, e.target.value)}
                        >
                          <option value="">请选择{group.label}</option>
                          {historical && (
                            <option value={value}>
                              {publishAttributeLabel(group, raw, product)}（历史值）
                            </option>
                          )}
                          {group.options.map((option) => (
                            <option key={option.value} value={option.value}>
                              {option.label}
                            </option>
                          ))}
                        </select>
                      </label>
                    );
                  })}
                  {form.mode === 'sale' ? (
                    <label className="form-field">
                      出售报价（元）
                      <input
                        inputMode="decimal"
                        value={price}
                        onChange={(e) => {
                          setPrice(e.target.value);
                          setDirty(true);
                        }}
                        maxLength={14}
                        placeholder="留空表示联系客服报价"
                      />
                    </label>
                  ) : (
                    <div className="form-field span-2">
                      <span>租赁报价（可多选）</span>
                      {form.unit === 'unknown' &&
                        product?.legacyPrice &&
                        !Object.keys(rentalPrices).length && (
                          <small>
                            原始参考价 {product.legacyPrice}
                            ，单位待核对。未填写新报价时保留历史参考信息。
                          </small>
                        )}
                      {Object.keys(rentalPrices)
                        .filter((unit) => !rentUnits.includes(unit as RentalQuote['unit']))
                        .map((unit) => (
                          <p key={unit} className="form-error">
                            {unitNames[unit as PriceUnit]} 已不在当前模板中。
                            <button
                              type="button"
                              className="text-button"
                              onClick={() => {
                                setRentalPrices((old) => {
                                  const next = { ...old };
                                  delete next[unit as RentalQuote['unit']];
                                  return next;
                                });
                                setDirty(true);
                              }}
                            >
                              移除此报价
                            </button>
                          </p>
                        ))}
                      <div className="rental-quote-editor">
                        {rentUnits.map((unit) => (
                          <div className="rental-quote-option" key={unit}>
                            <label className="quote-checkbox">
                              <input
                                type="checkbox"
                                checked={rentalPrices[unit] !== undefined}
                                onChange={(e) => {
                                  setRentalPrices((old) => {
                                    const next = { ...old };
                                    if (e.target.checked) next[unit] = '';
                                    else delete next[unit];
                                    return next;
                                  });
                                  setDirty(true);
                                }}
                              />
                              <span>{unitNames[unit]}</span>
                            </label>
                            {rentalPrices[unit] !== undefined && (
                              <input
                                aria-label={unitNames[unit] + '报价'}
                                inputMode="decimal"
                                value={rentalPrices[unit]}
                                onChange={(e) => {
                                  setRentalPrices((old) => ({ ...old, [unit]: e.target.value }));
                                  setDirty(true);
                                }}
                                maxLength={14}
                                placeholder="填写该单位报价"
                              />
                            )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                  {
                    <label className="form-field span-2">
                      自定义标签
                      <input
                        value={tags}
                        onChange={(e) => {
                          setTags(e.target.value);
                          setTagsEdited(true);
                          setDirty(true);
                        }}
                        maxLength={400}
                        placeholder="输入标签，用逗号分隔"
                      />
                    </label>
                  }
                  <label className="form-field span-2">
                    账号介绍
                    <textarea
                      aria-label="账号介绍"
                      value={form.description}
                      onChange={(e) => change('description', e.target.value)}
                      maxLength={10000}
                      placeholder="介绍账号配置、服饰与需要说明的情况"
                    />
                    <small>{form.description.length} / 10000</small>
                  </label>
                </div>
                {
                  <div className="publish-private">
                    <h3>私密交接资料</h3>
                    <p>
                      {product?.hasPrivateCredentials
                        ? '已保存，留空保留原值。'
                        : '仅平台核验，公开页面不展示。'}
                    </p>
                    <div className="form-grid">
                      <label className="form-field">
                        QQ账号
                        <input
                          value={qqAccount}
                          onChange={(e) => {
                            setQQAccount(e.target.value);
                            setDirty(true);
                          }}
                          inputMode="numeric"
                          autoComplete="off"
                          maxLength={20}
                          placeholder="填写需要交接的QQ账号"
                        />
                      </label>
                      <label className="form-field">
                        QQ密码
                        <input
                          type="password"
                          value={qqPassword}
                          onChange={(e) => {
                            setQQPassword(e.target.value);
                            setDirty(true);
                          }}
                          autoComplete="new-password"
                          maxLength={128}
                          placeholder="填写账号密码"
                        />
                      </label>
                    </div>
                  </div>
                }
              </section>
            </fieldset>
            <section className="publish-panel">
              <h2>02 · 展示图片</h2>
              <p>首次点击开始上传，会先保存草稿。图片可以持续追加、调整顺序与移除。</p>
              {product && dirty && (
                <p className="publish-hint">资料有未保存修改，请先保存草稿，再调整图片。</p>
              )}
              <ImageUploader
                product={product}
                ensureDraft={persist}
                onChange={updateGallery}
                onBusyChange={setUploadBusy}
                onPendingChange={setPendingImages}
                disabled={busy || conflict || (!!product && dirty)}
              />
            </section>
            {(!product || product.draftExpiresAt) && (
              <p className="profile-note" style={{ margin: '20px 0' }}>
                新上传图片最多暂存5天，草稿、审核中和退回修改均需及时处理。
              </p>
            )}
            {error && (
              <p className="form-error" role="alert">
                {error}
              </p>
            )}
            {conflict && (
              <button
                type="button"
                className="button-outline"
                disabled={busy || uploadBusy}
                onClick={() => {
                  if (!window.confirm('重新加载会替换未保存的编辑和待上传队列，继续？')) return;
                  if (!id && latest.current) navigate(`/me/listings/${latest.current.id}/edit`);
                  else setReload((value) => value + 1);
                }}
              >
                重新加载最新版本
              </button>
            )}
            <div className="publish-actions">
              <p>
                {uploadBusy ? (
                  '图片正在处理，请稍候'
                ) : product ? (
                  <>
                    <span className="mono">{product.code || product.id}</span> ·{' '}
                    {dirty ? '有未保存修改' : '草稿已保存'}
                  </>
                ) : (
                  '资料可以先保存，之后再继续整理。'
                )}
              </p>
              <div>
                {product && canDeleteDraft(product) && (
                  <button
                    type="button"
                    className="text-button delete-draft"
                    disabled={busy || uploadBusy}
                    onClick={() => void deleteDraft()}
                  >
                    删除草稿
                  </button>
                )}
                <button
                  type="button"
                  className="button-outline"
                  onClick={() => setPreview(true)}
                  disabled={busy || uploadBusy || conflict}
                >
                  上架预览
                </button>
                <button
                  type="button"
                  className="button-outline"
                  onClick={() => void save()}
                  disabled={busy || uploadBusy || conflict}
                >
                  {busy ? '正在保存…' : '保存草稿'}
                </button>
                <button
                  type="submit"
                  className="button-white"
                  disabled={busy || uploadBusy || pendingImages || conflict}
                  title={pendingImages ? '请先完成图片上传，或移除未完成的图片' : undefined}
                >
                  {busy ? '正在处理…' : '提交审核'} <span aria-hidden="true">↗</span>
                </button>
              </div>
            </div>
          </form>
        </>
      )}
      {preview && (
        <PublishPreview
          product={product}
          title={form.title}
          description={form.description}
          mode={form.mode}
          tags={[
            ...new Set([
              ...(Array.isArray(form.attributes?.tags) ? (form.attributes.tags as string[]) : []),
              ...(!tagsEdited && Array.isArray(latest.current?.attributes?.custom_tags)
                ? latest.current.attributes.custom_tags
                : tags
                    .split(/[,，、\n]/)
                    .map((tag) => tag.trim())
                    .filter(Boolean)),
            ]),
          ]}
          quotes={
            form.mode === 'rent'
              ? Object.entries(rentalPrices).map(([unit, amount]) => ({
                  unit: unit as RentalQuote['unit'],
                  amount: amount || '待填写',
                }))
              : []
          }
          price={price}
          fields={groups.map((group) => ({
            label: group.label,
            value: publishAttributeLabel(group, fieldValue(group.key), product),
          }))}
          onClose={() => setPreview(false)}
        />
      )}
    </section>
  );
}
