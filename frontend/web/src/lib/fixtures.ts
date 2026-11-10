import type { Category, ContentPage, Product } from '../types';

// The V3 concept artwork and all eight records are illustrative, never production inventory.
const rows = [
  [
    'QY-02418',
    '月白 · 极光典藏衣橱',
    0,
    4280,
    'account',
    'sale',
    '华东一区',
    10,
    'single',
    true,
    8,
    ['XVIP 王钻', '典藏 ×12', '外搭 ×560'],
  ],
  [
    'QY-02426',
    '曜影 · 黑金限定典藏',
    1,
    6880,
    'account',
    'sale',
    '华南一区',
    10,
    'linked',
    true,
    6,
    ['XVIP 王钻', '双号连体', '典藏 ×18'],
  ],
  [
    'QY-02431',
    '夜蔷薇 · 暗调高定衣橱',
    2,
    3280,
    'account',
    'sale',
    '华北一区',
    8,
    'single',
    false,
    7,
    ['VIP 8', '服饰 ×320', '单号独立'],
  ],
  [
    'QY-02439',
    '鎏光 · 蝶翼幻想典藏',
    3,
    8200,
    'account',
    'sale',
    '华东一区',
    10,
    'linked',
    true,
    3,
    ['XVIP 王钻', '典藏 ×26', '稀有翅膀'],
  ],
  [
    'QY-02445',
    '月白 · 流银盛装日租',
    0,
    180,
    'day',
    'rent',
    '华东一区',
    9,
    'single',
    false,
    5,
    ['VIP 9', '典藏 ×8', '按日租赁'],
  ],
  [
    'QY-02452',
    '曜影 · 礼服珍藏周租',
    1,
    680,
    'week',
    'rent',
    '华南一区',
    9,
    'single',
    false,
    4,
    ['VIP 9', '外搭 ×280', '按周租赁'],
  ],
  [
    'QY-02466',
    '夜蔷薇 · 新入藏风格衣橱',
    2,
    2680,
    'account',
    'sale',
    '华北一区',
    7,
    'single',
    false,
    10,
    ['VIP 7', '服饰 ×210', '资料待核验'],
  ],
  [
    'QY-02473',
    '鎏光 · 蝶翼典藏月租',
    3,
    1680,
    'month',
    'rent',
    '华东一区',
    10,
    'single',
    true,
    9,
    ['XVIP 王钻', '典藏 ×16', '按月租赁'],
  ],
] as const;
export const fixtureProducts: Product[] = rows.map(
  ([id, title, art, price, unit, mode, server, level, kind, rare, date, tags]) => ({
    id,
    code: id,
    title,
    art,
    price,
    unit,
    mode,
    server,
    level,
    kind,
    rare,
    tags: [...tags],
    gender: art === 1 ? 'male' : 'female',
    description:
      '此账号为界面演示样例，图片为概念艺术图，并非真实账号截图。正式商品的服饰、等级与绑定信息须以客服提供的实际资料为准。',
    status: 'published',
    version: 1,
    verification: 'unprovided',
    publishedAt: `2026-11-${String(date).padStart(2, '0')}T10:00:00+08:00`,
    imageTotal: 4,
    images: Array.from({ length: 4 }, (_, i) => ({
      id: `${id}-image-${i}`,
      art: (art + i) % 4,
      alt: `${title} · 概念图 ${i + 1}`,
    })),
  }),
);
export const editorialIds = ['QY-02418', 'QY-02426', 'QY-02473'];
const editorials = [
  {
    kicker: 'THE IVORY EDITION',
    name: '月白极光',
    collection: '典藏衣橱',
    description: '银白丝缎与羽饰，在细节中收藏你的审美。',
  },
  {
    kicker: 'THE NOIR EDITION',
    name: '曜影黑金',
    collection: '双号典藏',
    description: '利落剪裁，克制金饰。一份值得慢慢欣赏的珍藏。',
  },
  {
    kicker: 'THE BUTTERFLY EDITION',
    name: '鎏光蝶翼',
    collection: '月租甄选',
    description: '为下一段旅程，换一种出场方式。',
  },
];
fixtureProducts.forEach((p) => {
  const i = editorialIds.indexOf(p.id);
  if (i >= 0) p.editorial = editorials[i];
});
// Fixed V3 scores only demonstrate ordering. Live results are never re-ranked in the browser.
export const fixtureScores: Record<string, number> = {
  'QY-02418': 255,
  'QY-02426': 225,
  'QY-02431': 230,
  'QY-02439': 210,
  'QY-02445': 115,
  'QY-02452': 158,
  'QY-02466': 42,
  'QY-02473': 115,
};
export const fixtureCategories: Category[] = [
  {
    id: 'qq-dance',
    name: 'QQ 炫舞',
    priceUnits: ['account', 'hour', 'day', 'week', 'month'],
    filters: [
      {
        key: 'server',
        label: '游戏区服',
        options: ['华东一区', '华南一区', '华北一区'].map((value) => ({ value, label: value })),
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
        label: 'VIP 等级',
        options: [7, 8, 9, 10].map((value) => ({
          value: String(value),
          label: value === 10 ? 'XVIP 王钻' : `VIP ${value}`,
        })),
      },
      {
        key: 'kind',
        label: '账号结构',
        options: [
          { value: 'single', label: '单号独立' },
          { value: 'linked', label: '双号连体' },
        ],
      },
      {
        key: 'tag',
        label: '账号特点',
        options: [
          { value: '典藏', label: '典藏装扮' },
          { value: '稀有翅膀', label: '稀有翅膀' },
          { value: '外搭', label: '丰富外搭' },
        ],
      },
    ],
  },
];
export const fixturePages: Record<string, ContentPage> = {
  about: {
    slug: 'about',
    title: '关于清叶',
    published: true,
    body: '清叶围绕 QQ 炫舞账号的出售与租赁提供信息展示、筛选与客服咨询。\n\n当前网站为 React 前端演示，商品及概念图仅用于展示界面。真实商品、会员、客服、核验与图片存储服务尚未接入。',
  },
  publish: {
    slug: 'publish',
    title: '上架说明',
    published: true,
    body: '选择出售或租赁，填写账号资料与参考报价，并追加展示图片。\n\n资料可以先保存为草稿。提交后进入待审核状态，审核通过后才能公开展示。请勿在标题、介绍或图片中包含密码、验证码、身份证件等私密资料。\n\n图片不设商品总张数上限，请分批上传。演示模式下的草稿和图片仅保存在当前浏览器。',
  },
  process: {
    slug: 'process',
    title: '咨询与交付流程',
    published: true,
    body: '浏览账号与资料，记录商品编号，通过平台配置的客服渠道咨询。\n\n由客服协助确认实际资料、价格、使用条件与交付安排。页面不提供购物车、在线支付或自动租期管理。\n\n商品发布审核与账号资料核验是不同流程；审核通过不代表提供赔付保障。',
  },
  verification: {
    slug: 'verification',
    title: '资料核验说明',
    published: true,
    body: '资料核验用于说明核验时间、项目与结果，需要由真实业务服务提供记录。\n\n当前演示商品未提供真实核验档案，也不构成账号安全或赔付承诺。',
  },
  recommendation: {
    slug: 'recommendation',
    title: '推荐如何产生',
    published: true,
    body: '本期甄选由运营选择。热门推荐按有效浏览、有效收藏与首次公开时间的规则排序；最新上架按首次公开时间排序。\n\n演示版本使用固定样例数据呈现不同排序，未采集真实热度。正式排名将由 Go 服务返回，规则参数仍待经营方确认。',
  },
  terms: {
    slug: 'terms',
    title: '服务协议',
    published: false,
    body: '正式服务协议尚未由经营方提供。当前页面不构成生效协议，真实注册服务在协议配置完成前不可使用。',
  },
  privacy: {
    slug: 'privacy',
    title: '隐私说明',
    published: false,
    body: '正式隐私说明尚未由经营方提供。请勿在演示环境填写真实密码、验证码或身份资料。演示收藏、草稿和图片仅存储于当前浏览器，可通过清除站点数据移除。',
  },
};
fixturePages['listing-guide'] = fixturePages.publish;
fixturePages['service'] = fixturePages.process;
