#!/usr/bin/env python3
"""Seed editable test content via the API using private, explicitly supplied inputs.

Existing contacts and pages are preserved. This sends no messages to contacts.
"""
import argparse
import http.cookiejar
import json
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--base', required=True, help='API server origin, without /api/v1')
parser.add_argument('--origin', required=True, help='Allowed browser origin')
parser.add_argument('--secrets', required=True)
parser.add_argument('--contacts', required=True)
parser.add_argument('--archive', required=True, help='Private directory for original contact QR files')
args = parser.parse_args()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

def api(path, method='GET', body=None, data=None, content_type='application/json'):
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode()
    path = path if path.startswith('/api/') else '/api/v1' + path
    request = urllib.request.Request(args.base.rstrip('/') + path, data=data, method=method,
        headers={'Origin': args.origin, 'Content-Type': content_type})
    try:
        with opener.open(request, timeout=40) as response:
            payload = response.read()
            return json.loads(payload) if payload else {}
    except urllib.error.HTTPError as error:
        result = json.loads(error.read())
        raise RuntimeError(f'{method} {path.split("?")[0]}: {error.code} {result.get("code")} {result.get("message")}') from None

accounts = json.loads(Path(args.secrets).read_text())['accounts']
admin = next(account for account in accounts if account['role'] == 'admin')
api('/auth/login', 'POST', {key: admin[key] for key in ('phone', 'password')})
existing = api('/admin/contacts')['data']
known = {(row['name'], row['contact_value']) for row in existing}
archive = Path(args.archive)
archive.mkdir(parents=True, exist_ok=True)
created = 0
for index, contact in enumerate(json.loads(Path(args.contacts).read_text())):
    if (contact['name'], contact['contact_value']) in known:
        continue
    source = contact.pop('qr_source_url')
    parsed = urllib.parse.urlsplit(source)
    if parsed.scheme != 'https' or parsed.hostname != 'cdn.xuansiwei.com':
        raise ValueError('Unexpected contact QR source host')
    request = urllib.request.Request(source, headers={'Referer': 'https://qingyeyouxi.com/', 'User-Agent': 'Mozilla/5.0'})
    with urllib.request.urlopen(request, timeout=30) as response:
        data = response.read(10 * 1024**2 + 1)
    if len(data) > 10 * 1024**2:
        raise ValueError('Contact QR exceeds upload limit')
    mime, extension = ('image/png', '.png') if data.startswith(b'\x89PNG\r\n\x1a\n') else ('image/jpeg', '.jpg')
    filename = f'legacy-contact-{index + 1}{extension}'
    (archive/filename).write_bytes(data)
    ticket = api('/uploads', 'POST', {'filename': filename, 'mime_type': mime, 'byte_size': len(data)})['data']
    api(ticket['upload_url'], 'PUT', data=data, content_type=mime)
    api('/uploads/' + ticket['media_id'] + '/complete', 'POST')
    contact['qr_media_id'] = ticket['media_id']
    api('/admin/contacts', 'POST', contact)
    created += 1

pages = {
    'about': ('关于清叶', '清叶提供QQ炫舞账号展示与咨询服务。此站为自主建设项目的测试联调环境，商品来自旧站公开数据快照；请联系平台客服核实当前状态及报价。', True),
    'process': ('咨询流程', '选择账号后，在详情页复制商品编号并联系平台客服。旧站导入的价格保留原值，报价单位尚待核对。实际可用状态、费用及交付安排由客服进一步确认。本站当前不提供在线下单或支付。', True),
    'publish': ('上架说明', '测试账户登录后可填写账号资料、保存草稿并分批上传图片，再提交管理员审核。图片没有固定总张数上限；当前单张上传不超过10MB。已公开资料修改后会转为草稿，重新审核通过后展示。此处理规则用于本轮联调，正式经营规则仍需确认。', True),
    'verification': ('客服与核验', '页面客服资料取自旧站公开客服页面，可在本系统后台编辑。咨询时请提供商品编号。商品信息中的核验标识必须以后台实际核验状态为准；旧站导入不等于已核验。请勿在公开介绍或图片中填写密码、验证码与身份认证资料。', True),
    'recommendation': ('推荐说明', '人工精选由管理员维护；热门推荐采用近期有效浏览、首次收藏及新上架加分，参数可在后台修改并手动重算，也会定时更新。测试站使用本环境产生的数据，不把旧站累计浏览量当成近期热度。最新上架按公开时间排序。当前权重用于联调，正式规则待确认。', True),
    'terms': ('服务协议', '测试草稿：正式服务协议尚待经营方审核发布。当前不开放短信注册，不提供在线交易。', False),
    'privacy': ('隐私说明', '测试草稿：正式隐私政策尚待经营方审核发布。当前测试账户与数据仅用于前后端联调。', False),
}
existing_pages = {page['slug'] for page in api('/admin/pages')['data']}
for slug, (title, body, published) in pages.items():
    if slug not in existing_pages:
        api('/admin/pages/' + slug, 'PUT', {'title': title, 'body': body, 'published': published})

# Three test selections make the existing carousel reviewable. Do not overwrite curation.
featured = api('/home')['data']['featured']
selected = 0
if not featured:
    for rank, category in enumerate(('legacy-sale', 'legacy-short-rent', 'legacy-long-rent'), 1):
        candidates = api('/products?' + urllib.parse.urlencode({'category': category, 'limit': 20}))['data']
        product = next((item for item in candidates if item.get('images') and item['images'][0].get('url')), None)
        if product:
            api('/admin/products/' + product['id'] + '/featured', 'PUT', {'manual_rank': rank})
            selected += 1
api('/admin/recommendations/refresh', 'POST')
api('/auth/logout', 'POST')
print(json.dumps({'contacts_created': created, 'pages_created': len(set(pages) - existing_pages), 'test_featured_selected': selected}))
