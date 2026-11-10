import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { ArrowRight, ArrowUpRight } from 'lucide-react';
import { api, isDemo } from '../lib/api';
import { defaultHomePresentation, selectHomeSections } from '../lib/home-content';
import { useLoad } from '../lib-use';
import { AssetCard, StateView } from '../components/shared';
import { HomeEntrance } from '../components/HomeEntrance';
import { EditorialCarousel } from '../components/EditorialCarousel';

export function HomePage() {
  const result = useLoad(() => api.home(), 'home');
  const presentation = result.data?.presentation ?? defaultHomePresentation;
  const sections = useMemo(
    () => (result.data ? selectHomeSections(result.data, presentation.sections) : []),
    [result.data, presentation.sections],
  );
  return (
    <HomeEntrance>
      {result.loading || result.error ? (
        <div className="page-width mx-auto">
          <StateView {...result} onRetry={result.retry} />
        </div>
      ) : (
        result.data && (
          <>
            {result.data.featured.length > 0 && (
              <EditorialCarousel products={result.data.featured} presentation={presentation} />
            )}
            {sections.map(({ source: mode, title, description, products }) => {
              return (
                <section
                  className="recommendation-section home-product-section page-width mx-auto"
                  id={mode === 'hot' ? 'recommendations' : 'new-arrivals'}
                  aria-labelledby={`${mode}-heading`}
                  key={mode}
                >
                  <div className="section-heading">
                    <div>
                      <h2 id={`${mode}-heading`}>{title}</h2>
                      <p className="home-section-description">{description}</p>
                    </div>
                    <Link className="section-more" to={`/accounts?sort=${mode}`}>
                      查看全部 <ArrowRight />
                    </Link>
                  </div>
                  {products.length ? (
                    <div className="recommendation-grid home-product-grid" id={`${mode}-grid`}>
                      {products.map((p) => (
                        <AssetCard key={p.id} product={p} />
                      ))}
                    </div>
                  ) : (
                    <StateView empty={`暂无${title}`} />
                  )}
                  {mode === 'hot' && (
                    <Link
                      className="recommendation-explainer home-ranking-note"
                      to="/help/recommendation"
                    >
                      {isDemo ? '演示排序说明' : '了解推荐规则'} <ArrowUpRight />
                    </Link>
                  )}
                </section>
              );
            })}
          </>
        )
      )}
      <section className="sell-banner page-width mx-auto">
        <div>
          <h2>{presentation.publishing.title}</h2>
          <p>{presentation.publishing.description}</p>
        </div>
        <Link className="button-white" to="/publish">
          {presentation.publishing.action} <ArrowUpRight />
        </Link>
        <div className="banner-orbit" />
      </section>
    </HomeEntrance>
  );
}
