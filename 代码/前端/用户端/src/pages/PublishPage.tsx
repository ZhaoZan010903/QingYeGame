import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, isDemo, request } from '../lib/api';
import type { Category, PriceUnit, Product, ProductWrite } from '../types';
import { useUi } from '../context';
import { ImageUploader } from '../components/ImageUploader';
import '../member.css';
import '../service-refinement.css';
import { Modal } from '../components/shared';
import {
  defaultPublishForm,
  resolvePublishForm,
  type PublishFormConfig,
} from '../../../共享/publish-form';
import type { FilterGroup, RentalQuote } from '../types';
import { missingPublishField, publishAttributes, rentalPriceForWrite } from '../lib/publish-fields';

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
  const [settings, setSettings] = useState<PublishFormConfig>(defaultPublishForm);
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
    Promise.all([
      api.categories(),
      id ? api.myProduct(id) : Promise.resolve(null),
      isDemo
        ? Promise.resolve(defaultPublishForm)
        : request('/publish-form').then((r) => resolvePublishForm(r.data)),
    ])
      .then(([list, value, config]) => {
        if (!active) return;
        setCategories(list);
        setSettings(config);
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
    const attributes = publishAttributes(
      form,
      categories.find((item) => item.id === form.categoryId)?.filters ?? [],
    );
    const customTags = tags
      .split(/[,，、\n]/)
      .map((tag) => tag.trim())
      .filter(Boolean);
    if (customTags.length > 12 || customTags.some((tag) => Array.from(tag).length > 24))
      throw new Error('自定义标签最多12个，每个最多24字。');
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
    if (saving.current) return saving.current;
    const data = input();
    if (!data.categoryId) throw new Error('尚未配置可发布分类，请联系客服。');
    const p = latest.current;
    const operation = api.saveProduct(data, p?.id, p?.version);
    saving.current = operation;
    try {
      const next = await operation;
      updateProduct(next);
      setForm((previous) => ({ ...previous, unit: next.unit, price: next.price }));
      setQQAccount('');
      setQQPassword('');
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
        if (!data.title || !data.description)
          throw new Error('提交审核前，请填写账号标题和账号介绍。');
        if (
          settings.fields.private_credentials.enabled &&
          settings.fields.private_credentials.required &&
          !data.privateCredentials &&
          !latest.current?.hasPrivateCredentials
        )
          throw new Error('请填写账号交接资料。');
        if (
          settings.fields.custom_tags.enabled &&
          settings.fields.custom_tags.required &&
          !data.tags.length
        )
          throw new Error('请填写至少一个自定义标签。');
        const missing = missingPublishField(
          data,
          categories.find((c) => c.id === form.categoryId)?.filters ?? [],
        );
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
  const category = categories.find((c) => c.id === form.categoryId);
  const groups = (category?.filters ?? []).filter(
    (g) =>
      g.enabled !== false &&
      ![
        'tags',
        'game',
        'custom_tags',
        'private_credentials',
        'qq_account',
        'qq_password',
        'qqAccount',
        'qqPassword',
        'password',
      ].includes(g.key),
  );
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
  const locked = isDemo && (product?.status === 'published' || product?.status === 'pending');
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
            <fieldset disabled={busy || uploadBusy} style={{ border: 0, padding: 0, minWidth: 0 }}>
              <section className="publish-panel">
                <h2>01 · 账号资料</h2>
                <p>展示资料与私密交接信息分开填写。保存后自动生成商品编号。</p>
                <div className="publish-identity">
                  <div>
                    <strong>QQ炫舞 · {form.mode === 'sale' ? '账号出售' : '账号租赁'}</strong>
                    <small>统一游戏资料，由平台配置发布选项</small>
                  </div>
                  <div>
                    <strong>
                      {product?.code ||
                        (form.mode === 'sale' ? 'QYCH · 保存后自动分配' : 'QYZH · 保存后自动分配')}
                    </strong>
                    <small>承接历史编号，系统分配唯一新编号</small>
                  </div>
                </div>
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
                          {mode === 'sale' ? '账号出售' : '账号租赁'}
                        </label>
                      ))}
                    </div>
                    {product && <small>租售类型已固定，如需更换请新建资产。</small>}
                  </div>
                  <label className="form-field span-2">
                    {settings.fields.title.label}
                    <input
                      value={form.title}
                      onChange={(e) => change('title', e.target.value)}
                      maxLength={1000}
                      placeholder={settings.fields.title.placeholder}
                    />
                    <small>最多1000字，详细配置也可以放在账号介绍中。</small>
                  </label>
                  {groups.length
                    ? groups.map((group) => (
                        <div className="form-field" key={group.key}>
                          <span>
                            {group.label}
                            {group.required ? ' *' : ''}
                          </span>
                          {group.type === 'text' || !group.options.length ? (
                            <input
                              aria-label={group.label}
                              value={String(fieldValue(group.key))}
                              maxLength={200}
                              placeholder={group.placeholder || '填写' + group.label}
                              onChange={(e) => setField(group, e.target.value)}
                            />
                          ) : group.multiple || group.type === 'multiselect' ? (
                            <div className="attribute-options">
                              {group.options.map((option) => (
                                <label className="agreement-check" key={option.value}>
                                  <input
                                    type="checkbox"
                                    checked={
                                      Array.isArray(fieldValue(group.key)) &&
                                      (fieldValue(group.key) as string[]).includes(option.value)
                                    }
                                    onChange={(e) => {
                                      const value = Array.isArray(fieldValue(group.key))
                                        ? (fieldValue(group.key) as string[])
                                        : [];
                                      setField(
                                        group,
                                        e.target.checked
                                          ? [...value, option.value]
                                          : value.filter((v) => v !== option.value),
                                      );
                                    }}
                                  />
                                  {option.label}
                                </label>
                              ))}
                            </div>
                          ) : (
                            <select
                              aria-label={group.label}
                              value={String(fieldValue(group.key))}
                              onChange={(e) => setField(group, e.target.value)}
                            >
                              <option value="">
                                {group.placeholder || '请选择' + group.label}
                              </option>
                              {group.options.map((option) => (
                                <option key={option.value} value={option.value}>
                                  {option.label}
                                </option>
                              ))}
                            </select>
                          )}
                        </div>
                      ))
                    : null}
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
                      <span>租赁报价 · 可以选择多个单位</span>
                      <small>分别填写实际报价，不自动换算；第一项用作列表参考价。</small>
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
                              {unitNames[unit]}
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
                  {settings.fields.custom_tags.enabled && (
                    <label className="form-field span-2">
                      {settings.fields.custom_tags.label}
                      <input
                        value={tags}
                        onChange={(e) => {
                          setTags(e.target.value);
                          setDirty(true);
                        }}
                        maxLength={200}
                        placeholder={settings.fields.custom_tags.placeholder}
                      />
                      <small>用顿号或逗号分隔，最多12个标签，每个最多24字。</small>
                      {Array.isArray(form.attributes?.tags) && form.attributes.tags.length > 0 && (
                        <small>
                          另有 {form.attributes.tags.length}{' '}
                          个历史标签，会保留原始资料；此处只编辑新增的自定义标签。
                        </small>
                      )}
                    </label>
                  )}
                  <label className="form-field span-2">
                    {settings.fields.description.label}
                    <textarea
                      value={form.description}
                      onChange={(e) => change('description', e.target.value)}
                      maxLength={10000}
                      placeholder={settings.fields.description.placeholder}
                    />
                    <small>{form.description.length} / 10000</small>
                  </label>
                </div>
                {settings.fields.private_credentials.enabled && (
                  <div className="publish-private">
                    <h3>{settings.fields.private_credentials.label}</h3>
                    <p>
                      {settings.fields.private_credentials.placeholder}{' '}
                      {product?.hasPrivateCredentials
                        ? '已保存交接资料；留空保留原值，填写两项后替换。'
                        : '账号和密码不会出现在公开详情、搜索或上架预览中。'}
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
                )}
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
                  onClick={() => setPreview(true)}
                  disabled={busy || uploadBusy}
                >
                  上架预览
                </button>
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
      {preview && (
        <Modal
          title="上架预览"
          onClose={() => setPreview(false)}
          className="publish-preview-dialog"
        >
          <div className="publish-preview">
            {product?.images[0] && (
              <img
                className="preview-cover"
                src={product.images[0].thumbnailUrl ?? product.images[0].url}
                alt="展示封面"
              />
            )}
            <small>{product?.code || '保存后生成编号'} · QQ炫舞</small>
            <h3>{form.title || '尚未填写标题'}</h3>
            <div className="preview-meta">
              <span>{form.mode === 'sale' ? '账号出售' : '账号租赁'}</span>
              <span>{String(fieldValue('server')) || '未填写区服'}</span>
            </div>
            <dl className="preview-attributes">
              {groups
                .filter((group) => group.key !== 'server')
                .map((group) => {
                  const raw = fieldValue(group.key);
                  const values = Array.isArray(raw) ? raw : [raw];
                  const label = values
                    .map(
                      (value) =>
                        group.options.find((option) => option.value === String(value))?.label ??
                        String(value ?? ''),
                    )
                    .filter(Boolean)
                    .join('、');
                  return label ? (
                    <div key={group.key}>
                      <dt>{group.label}</dt>
                      <dd>{label}</dd>
                    </div>
                  ) : null;
                })}
            </dl>
            <div className="pill-tags">
              {tags
                .split(/[,，、\n]/)
                .map((t) => t.trim())
                .filter(Boolean)
                .map((tag, i) => (
                  <span key={i}>{tag}</span>
                ))}
            </div>
            {form.mode === 'rent' ? (
              Object.keys(rentalPrices).length ? (
                Object.entries(rentalPrices).map(([unit, value]) => (
                  <strong key={unit}>
                    {value || '待填写'} {unitNames[unit as PriceUnit]}
                  </strong>
                ))
              ) : (
                <strong>
                  {form.unit === 'unknown' && product?.legacyPrice
                    ? `原始参考价 ${product.legacyPrice} · 报价单位待核对`
                    : '联系客服报价'}
                </strong>
              )
            ) : (
              <strong>
                {price || '联系客服'} {price ? '元' : ''}
              </strong>
            )}
            <p>{form.description || '尚未填写介绍'}</p>
            <small>预览不会提交审核。仅展示公开内容，完整图片以已保存图集为准。</small>
          </div>
        </Modal>
      )}
    </section>
  );
}
