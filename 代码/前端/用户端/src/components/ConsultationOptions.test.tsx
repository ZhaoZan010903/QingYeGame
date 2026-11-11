import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { ConsultationOptions } from './ConsultationOptions';
import { ContactDetails } from './ContactDetails';
import { contactsForChannel } from '../lib/contact-options';
import type { Contact } from '../types';

const qq: Contact = {
  id: 'c1',
  name: '客服一',
  channel: 'qq',
  value: '12345',
  qrUrl: '/api/v1/media/qr/file',
  purpose: ['sale'],
};
describe('商品咨询入口', () => {
  it('在线咨询传递真实商品 ID，QQ 入口为独立按钮', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <ConsultationOptions
          productId="real-id"
          code="QY-001"
          title="测试账号"
          onClose={() => {}}
          onQQ={() => {}}
        />
      </MemoryRouter>,
    );
    expect(html).toContain('href="/messages?product=real-id"');
    expect(html).not.toContain('product=QY-001');
    expect(html).toContain('在线咨询客服');
    expect(html).toContain('联系 QQ 客服');
    expect(html).toContain('无需登录');
  });
  it('QQ 过滤依据渠道，避免名称带 QQ 的微信二维码混入', () => {
    const wechat = { ...qq, id: 'c2', name: 'QQ 加不上，加微信', channel: 'wechat' };
    const second = { ...qq, id: 'c3', channel: ' QQ ' };
    expect(contactsForChannel([wechat, qq, second], 'qq')).toEqual([qq, second]);
    expect(contactsForChannel([wechat, qq])).toHaveLength(2);
    expect(contactsForChannel([wechat], 'qq')).toEqual([]);
  });
  it('二维码缺失时仍提供号码，未生成替代二维码', () => {
    const html = renderToStaticMarkup(
      <ContactDetails contact={{ ...qq, qrUrl: undefined }} notify={() => {}} />,
    );
    expect(html).toContain('暂未上传二维码');
    expect(html).toContain('12345');
    expect(html).toContain('复制QQ联系方式');
    expect(html).not.toContain('<img');
  });
});
