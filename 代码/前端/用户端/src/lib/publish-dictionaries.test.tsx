import { describe, expect, it, vi, afterEach } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  canDeleteDraft,
  customPublishTags,
  fixedPublishFields,
  publishAttributeLabel,
} from './publish-dictionaries';
import { productFrom, api } from './api';
import { PublishPreview } from '../components/PublishPreview';

afterEach(() => vi.unstubAllGlobals());
describe('固定发布表单', () => {
  it('未改的停用历史值使用商品显示名，切换后不沿用旧名称', () => {
    const group = {
      key: 'server',
      label: '区服',
      options: [{ value: 'server_new', label: '新大区' }],
    };
    const product = productFrom({
      id: 'old',
      attributes: { server: 'server_closed' },
      attribute_labels: { server: '旧大区' },
    });
    expect(publishAttributeLabel(group, 'server_closed', product)).toBe('旧大区');
    expect(publishAttributeLabel(group, 'server_new', product)).toBe('新大区');
    expect(publishAttributeLabel(group, '', product)).toBe('');
  });
  it('交易类型共用字典，三个属性始终单选且只有区服性别必填', () => {
    const fields = fixedPublishFields(
      [
        {
          key: 'server',
          label: '区服',
          version: 1,
          options: [
            { value: 'one', label: '共用一区' },
            { value: 'closed', label: '停用区', enabled: false },
          ],
        },
      ],
      [],
    );
    expect(fields.map((field) => field.key)).toEqual(['server', 'gender', 'level']);
    expect(fields.every((field) => field.type === 'select' && !field.multiple)).toBe(true);
    expect(fields.filter((field) => field.required).map((field) => field.key)).toEqual([
      'server',
      'gender',
    ]);
    expect(fields[0].options.map((option) => option.value)).toEqual(['one']);
  });
  it('旧API过渡时合并租售区服，不按当前交易模板换选项', () => {
    const fields = fixedPublishFields(
      [],
      ['sale', 'rent'].map((tradeType, i) => ({
        id: tradeType,
        name: tradeType,
        tradeType: tradeType as 'sale' | 'rent',
        priceUnits: [],
        filters: [
          {
            key: 'server',
            label: '区服',
            multiple: true,
            options: [{ value: String(i), label: '区' + i }],
          },
        ],
      })),
    );
    expect(fields[0].options).toHaveLength(2);
    expect(fields[0].multiple).toBe(false);
  });
  it('标签按Unicode字数计算，20个5字可用，超出清楚报错', () => {
    expect(customPublishTags('😀😀😀😀😀,白金典藏')).toEqual(['😀😀😀😀😀', '白金典藏']);
    expect(
      customPublishTags(Array.from({ length: 20 }, (_, i) => '标签' + i).join(',')),
    ).toHaveLength(20);
    expect(() => customPublishTags('😀😀😀😀😀😀')).toThrow('最多5个字');
    expect(() =>
      customPublishTags(Array.from({ length: 21 }, (_, i) => '标签' + i).join(',')),
    ).toThrow('最多20个');
  });
  it('未编辑的历史长标签原样保留，主动编辑后执行新限制', () => {
    const original = ['历史较长的自定义标签', '含、顿号'];
    expect(customPublishTags(original.join('、'), original)).toEqual(original);
    expect(() => customPublishTags('历史较长的自定义标签')).toThrow('最多5个字');
    expect(customPublishTags('新标签')).toEqual(['新标签']);
  });
  it('只有从未上架的新草稿显示永久删除入口', () => {
    const draft = productFrom({
      id: 'draft',
      status: 'draft',
      draft_expires_at: '2026-11-15T00:00:00Z',
    });
    expect(canDeleteDraft(draft)).toBe(true);
    expect(canDeleteDraft({ ...draft, source: 'legacy' })).toBe(false);
    expect(canDeleteDraft({ ...draft, publishedAt: '2026-11-01' })).toBe(false);
    expect(canDeleteDraft({ ...draft, status: 'pending' })).toBe(false);
    expect(canDeleteDraft({ ...draft, draftExpiresAt: null })).toBe(false);
  });
  it('删除请求携带版本，失败不会伪装成功', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ message: '版本冲突' }), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetch);
    await expect(api.deleteDraft('draft-1', 5)).rejects.toThrow('版本冲突');
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/me/products/draft-1');
    expect(fetch.mock.calls[0][1].method).toBe('DELETE');
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ expected_version: 5 });
  });
  it('详情式预览显示全部文案和租价，私密资料不进入预览结构', () => {
    const product = productFrom({
      id: 'draft',
      title: '旧标题',
      private_credentials: { qq_password: 'private-secret' },
      attributes: {},
      images: [],
    });
    const html = renderToStaticMarkup(
      <PublishPreview
        product={product}
        title={'完整标题'.repeat(80)}
        description="全部介绍"
        mode="rent"
        tags={['典藏', '稀有']}
        price=""
        quotes={[
          { unit: 'day', amount: '20' },
          { unit: 'month', amount: '350' },
        ]}
        fields={[{ label: '区服', value: '一区' }]}
        onClose={() => {}}
      />,
    );
    expect(html).toContain('完整标题'.repeat(80));
    expect(html).toContain('350');
    expect(html).toContain('元 / 月');
    expect(html).not.toContain('private-secret');
    expect(html).not.toContain('QQ密码');
  });
});
