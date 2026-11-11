import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { AssetCard } from '../components/shared';
import { productFrom, productToWire } from './api';
import {
  readHomePresentation,
  resolveHomePresentation,
  type HomeSlide,
} from '../../../共享/home-content';
import { resolvePublishForm } from '../../../共享/publish-form';
import { CarouselSlidePreview } from '../../../共享/CarouselSlidePreview';
import { missingPublishField, publishAttributes, rentalPriceForWrite } from './publish-fields';
import type { FilterGroup, ProductWrite } from '../types';

const slide: HomeSlide = {
  productId: 'product-1',
  title: '手工策划标题',
  description: '独立编辑的介绍',
  tradeLabel: '账号租赁',
  serverLabel: '一舞时光',
  priceLabel: '85 元 / 日',
  imagePositionX: 25,
  imagePositionY: 70,
  imageFit: 'contain',
  layout: 'image-left',
  textAlign: 'right',
  verticalAlign: 'bottom',
};
describe('手工首页装修与预览', () => {
  it('没有手工配置时不会生成商品轮播', () => expect(resolveHomePresentation().slides).toEqual([]));
  it('保存并解析布局、独立文案和图片焦点', () =>
    expect(readHomePresentation({ slides: [slide] }).slides).toMatchObject([slide]));
  it('拒绝重复商品与非法布局值，避免编辑器预览和线上结果不同', () => {
    expect(() => readHomePresentation({ slides: [slide, slide] })).toThrow();
    expect(() => readHomePresentation({ slides: [{ ...slide, imagePositionX: 101 }] })).toThrow();
    expect(() => readHomePresentation({ slides: [{ ...slide, layout: 'arbitrary' }] })).toThrow();
  });
  it('预览渲染管理员文案并转义文字，不执行HTML', () => {
    const html = renderToStaticMarkup(
      <CarouselSlidePreview
        slide={{ ...slide, title: '<script>alert(1)</script>' }}
        imageUrl="/api/v1/media/example"
      />,
    );
    expect(html).toContain('独立编辑的介绍');
    expect(html).toContain('85 元 / 日');
    expect(html).toContain('layout-image-left');
    expect(html).toContain('--slide-x:25%');
    expect(html).not.toContain('<script>');
  });
});

describe('动态表单与历史配置兼容', () => {
  it('未知单位普通编辑不误标日价，只有明确报价后才修改单位', () => {
    expect(rentalPriceForWrite([], 'unknown', ['day', 'month'])).toEqual({
      unit: 'unknown',
      price: null,
    });
    expect(
      rentalPriceForWrite([{ unit: 'month', amount: '450.00' }], 'unknown', ['day', 'month']),
    ).toEqual({ unit: 'month', price: 450 });
    expect(rentalPriceForWrite([], 'day', ['day'])).toEqual({ unit: 'day', price: null });
  });
  const form: ProductWrite = {
    title: '账号',
    mode: 'sale',
    unit: 'account',
    price: null,
    server: '',
    gender: 'female',
    level: 0,
    kind: 'single',
    tags: [],
    description: '介绍',
  };
  const server: FilterGroup = {
    key: 'server',
    label: '所在区服',
    type: 'select',
    required: true,
    options: [{ label: '一区', value: 'one' }],
  };
  it('关闭或取消必填的区服不会阻止提交', () => {
    expect(missingPublishField(form, [{ ...server, enabled: false }])).toBeUndefined();
    expect(missingPublishField(form, [{ ...server, required: false }])).toBeUndefined();
    expect(missingPublishField(form, [])).toBeUndefined();
    expect(missingPublishField(form, [server])?.key).toBe('server');
  });
  it('从下拉改为文本时保留不在旧选项中的文字', () => {
    const source = { ...form, server: '新增的区服' };
    expect(publishAttributes(source, [{ ...server, type: 'text' }]).server ?? source.server).toBe(
      '新增的区服',
    );
    expect(publishAttributes(source, [server]).server).toBe('');
  });
  it('动态多选必须有值，数值零仍是有效输入', () => {
    expect(
      missingPublishField({ ...form, attributes: { level: 0 } }, [{ ...server, key: 'level' }]),
    ).toBeUndefined();
    expect(
      missingPublishField({ ...form, attributes: { features: [] } }, [
        { ...server, key: 'features', type: 'multiselect' },
      ])?.key,
    ).toBe('features');
  });
  it('内置多选值逐项校验，不会把有效数组转为逗号文本再清空', () => {
    const result = publishAttributes({ ...form, attributes: { server: ['one', 'removed'] } }, [
      { ...server, type: 'multiselect', multiple: true },
    ]);
    expect(result.server).toEqual(['one']);
  });
});
describe('发布输入与私密资料边界', () => {
  it('字典名称用于展示，编辑和提交仍保留原始属性编码', () => {
    const product = productFrom({
      id: 'dictionary-product',
      attributes: { server: 'server_east', gender: 'female', level: 'diamond', kind: 'linked' },
      attribute_labels: { server: '华东区', gender: '女角色', level: '金钻', kind: '双号连体' },
    });
    expect(product.server).toBe('华东区');
    expect(product.attributeLabels).toEqual({
      server: '华东区',
      gender: '女角色',
      level: '金钻',
      kind: '双号连体',
    });
    expect(product.gender).toBe('female');
    expect(product.kind).toBe('linked');
    const card = renderToStaticMarkup(
      <MemoryRouter>
        <AssetCard product={product} />
      </MemoryRouter>,
    );
    expect(card).toContain('华东区');
    expect(card).toContain('女角色');
    expect(card).not.toContain('server_east');
    expect(productToWire(product).attributes).toMatchObject({
      server: 'server_east',
      gender: 'female',
      level: 'diamond',
      kind: 'linked',
    });
  });
  const product = productFrom({
    id: 'product-1',
    title: '商品',
    trade_type: 'rent',
    price_unit: 'day',
    price_amount: '85',
    attributes: { server: '一舞时光' },
    private_credentials: { qq_account: '123456', qq_password: 'never-public' },
    has_private_credentials: true,
    rental_quotes: [
      { unit: 'day', amount: '85.00' },
      { unit: 'month', amount: '450.00' },
    ],
    consultation_count: 7,
  });
  it('公开商品模型不包含后台私密明文', () => {
    expect(JSON.stringify(product)).not.toContain('never-public');
    expect(product.hasPrivateCredentials).toBe(true);
  });
  it('保留不同租期的独立金额与咨询人数', () => {
    expect(product.rentalQuotes).toHaveLength(2);
    expect(product.rentalQuotes?.[1].amount).toBe('450.00');
    expect(product.consultationCount).toBe(7);
  });
  it('私密资料仅写入专用请求字段，不进入公开attributes', () => {
    const payload = productToWire({
      ...product,
      privateCredentials: { qqAccount: '123456', qqPassword: 'never-public' },
    });
    expect(payload.private_credentials).toEqual({
      qq_account: '123456',
      qq_password: 'never-public',
    });
    expect(JSON.stringify(payload.attributes)).not.toContain('never-public');
    expect(payload.rental_quotes).toHaveLength(2);
  });
  it('不填写私密资料时省略字段以保留服务器原值', () =>
    expect(productToWire(product)).not.toHaveProperty('private_credentials'));
  it('新旧标签独立存储，清空自定义标签不会破坏历史资料', () => {
    expect(
      productFrom({ id: 'p', attributes: { custom_tags: ['自定义'], tags: ['旧标签'] } }).tags,
    ).toEqual(['旧标签', '自定义']);
    expect(
      productFrom({ id: 'p', attributes: { custom_tags: [], tags: ['旧标签'] } }).tags,
    ).toEqual(['旧标签']);
    expect(productFrom({ id: 'p', attributes: { tags: ['旧标签'] } }).tags).toEqual(['旧标签']);
    const wire = productToWire({
      ...product,
      tags: [],
      attributes: { tags: ['旧标签'], custom_tags: ['待清空'] },
    });
    expect(wire.attributes.custom_tags).toEqual([]);
    expect(wire.attributes.tags).toEqual(['旧标签']);
  });
  it('出售模式不会发送租赁报价', () =>
    expect(productToWire({ ...product, mode: 'sale', unit: 'account' }).rental_quotes).toEqual([]));
  it('管理配置可改文案及可选字段，但不能关闭核心资料要求', () => {
    const settings = resolvePublishForm({
      fields: {
        title: { label: '展示标题', enabled: false, required: false },
        custom_tags: { enabled: false },
        private_credentials: { required: true },
      },
    });
    expect(settings.fields.title.label).toBe('展示标题');
    expect(settings.fields.title.enabled).toBe(true);
    expect(settings.fields.title.required).toBe(true);
    expect(settings.fields.custom_tags.enabled).toBe(false);
    expect(settings.fields.private_credentials.required).toBe(true);
  });
});
