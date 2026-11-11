import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { AccountGallery } from './AccountGallery';
import { AssetArchive } from './AssetArchive';
import { Price } from './shared';
import { productFrom } from '../lib/api';
import { assetParagraphs, assetTokens, quoteTerms } from '../lib/asset-copy';

describe('大量图片与长文的详情展示', () => {
  const title = '账号详细配置说明与珍藏外观，'.repeat(20);
  const product = productFrom({
    id: 'account-test',
    title,
    description: '完整卖家说明。',
    price_amount: 330,
    price_unit: 'month',
    images: Array.from({ length: 225 }, (_, n) => ({
      id: `i-${n}`,
      url: `/original/${n}`,
      thumbnail_url: `/thumb/${n}`,
    })),
    image_total: 225,
  });
  it('225 张资料仅在页面流渲染 12 张缩略图，并保留全部图集入口', () => {
    const html = renderToStaticMarkup(
      <AccountGallery product={product} reloadProduct={() => {}} />,
    );
    expect(html.match(/aria-label="查看第 \d+ 张图片"/g)).toHaveLength(12);
    expect(html).toContain('展开查看全部');
    expect(html).toContain('225');
    expect(html).not.toContain('/thumb/224');
  });
  it('两行摘要之外的完整长标题和卖家说明在资产档案保留', () => {
    const html = renderToStaticMarkup(<AssetArchive product={product} />);
    expect(html).toContain(title);
    expect(html).toContain('完整卖家说明。');
  });
  it('紧凑价格使用月单位，不修改真实报价或未知单位说明', () => {
    expect(renderToStaticMarkup(<Price product={product} compact />)).toContain('/ 月');
    const unknown = {
      ...product,
      unit: 'unknown' as const,
      legacyPrice: '待询价',
      priceNote: '单位未核对',
    };
    expect(renderToStaticMarkup(<Price product={unknown} compact />)).toContain('单位未核对');
  });
  it('仅提取明确报价，范围、单位数量和冲突金额不生成单价', () => {
    expect(quoteTerms('押金200 日租：45元 包夜￥80 周租300-400 月租1个月')).toEqual([
      { label: '押金', value: '200' },
      { label: '日租', value: '45' },
      { label: '包夜', value: '80' },
    ]);
    expect(quoteTerms('日租50元 日租60元')).toEqual([]);
  });
  it('道具列表可以分块，报价和原始长句不会被误拆成标签', () => {
    expect(assetTokens('永夜头、星河衣、银月翼、珍珠鞋、白鹿套、星海帽')).toHaveLength(6);
    expect(assetTokens('押金200，日租45，包夜80，星河衣，银月翼，白鹿套')).toBeNull();
    const sentence = '珍藏道具'.repeat(25) + '。完整说明。';
    expect(assetParagraphs(sentence).join('')).toBe(sentence);
  });
});
