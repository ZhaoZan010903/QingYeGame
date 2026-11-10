import { Link, useParams } from 'react-router-dom';
import { ArrowRight } from 'lucide-react';
import { api } from '../lib/api';
import { useLoad } from '../lib-use';
import { StateView } from '../components/shared';
import { useUi } from '../context';
export function HelpPage() {
  const { slug = 'about' } = useParams();
  const result = useLoad(() => api.page(slug), slug);
  const { contact } = useUi();
  return (
    <div className="help-page page-width mx-auto">
      <aside className="help-menu">
        {[
          ['about', '关于清叶'],
          ['process', '咨询流程'],
          ['publish', '上架说明'],
          ['verification', '客服与核验'],
          ['recommendation', '推荐说明'],
          ['terms', '服务协议'],
          ['privacy', '隐私说明'],
        ].map(([path, label]) => (
          <Link className={path === slug ? 'active' : ''} key={path} to={`/help/${path}`}>
            {label}
            <ArrowRight />
          </Link>
        ))}
      </aside>
      <article className="panel help-content">
        {result.loading || result.error ? (
          <StateView {...result} onRetry={result.retry} />
        ) : (
          result.data && (
            <>
              <h1>{result.data.title}</h1>
              {!result.data.published && (
                <p className="notice-box">此页面尚未发布正式内容，服务及协议条款需由经营方确认。</p>
              )}
              <div className="plain-content">{result.data.body}</div>
              <button
                className="button-outline"
                onClick={() => contact(undefined, slug === 'publish' ? 'publish' : undefined)}
              >
                联系客服 <ArrowRight />
              </button>
            </>
          )
        )}
      </article>
    </div>
  );
}
