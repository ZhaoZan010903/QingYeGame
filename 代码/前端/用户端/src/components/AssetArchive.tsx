import type { Product } from '../types';
import { assetParagraphs, assetTokens } from '../lib/asset-copy';

export function AssetArchive({ product }: { product: Product }) {
  const titleInDescription = product.description.includes(product.title);
  const paragraphs = assetParagraphs(product.description);
  return (
    <section className="asset-archive" id="asset-archive" aria-labelledby="asset-archive-title">
      <header>
        <span className="archive-kicker">ASSET DOSSIER</span>
        <h2 id="asset-archive-title">资产配置清单与详细说明</h2>
      </header>
      {!titleInDescription && (
        <div className="archive-title-copy">
          <h3>完整商品标题</h3>
          {assetParagraphs(product.title).map((line, i) => (
            <p key={i}>{line}</p>
          ))}
        </div>
      )}
      {product.tags.length > 0 && (
        <div className="archive-tags" aria-label="商品属性">
          {product.tags.map((tag, i) => (
            <span key={i}>{tag}</span>
          ))}
        </div>
      )}
      <div className="archive-description">
        {paragraphs.length ? (
          paragraphs.map((line, i) => {
            const tokens = assetTokens(line);
            return tokens ? (
              <div className="archive-tags" key={i}>
                {tokens.map((token, j) => (
                  <span key={j}>{token}</span>
                ))}
              </div>
            ) : (
              <p key={i}>{line}</p>
            );
          })
        ) : (
          <p>暂无补充介绍，请联系客服了解详情。</p>
        )}
      </div>
      <p className="archive-note">以上为商品发布资料；账号绑定、租期及交付条件请向客服确认。</p>
    </section>
  );
}
