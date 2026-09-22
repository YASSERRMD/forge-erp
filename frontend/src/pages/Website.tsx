import { useEffect, useState } from 'react';
import { Globe } from 'lucide-react';
import { apiExt, type WebsitePage } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Website() {
  const { token } = useAuth();
  const { t } = useLang();
  const [pages, setPages] = useState<WebsitePage[]>([]);
  const [slug, setSlug] = useState('');
  const [view, setView] = useState<WebsitePage | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (token) apiExt.websitePages(token).then(setPages).catch(() => undefined);
  }, [token]);

  useEffect(() => {
    if (token && slug) {
      apiExt
        .websitePage(token, slug)
        .then((p) => {
          setView(p);
          setError('');
        })
        .catch((e: Error) => setError(e.message));
    } else {
      setView(null);
    }
  }, [token, slug]);

  return (
    <div>
      <PageHeader icon={<Globe size={22} />} title={t('website')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('publishedPages')}>
        <label className="field">
          {t('publishedPages')}{' '}
          <select value={slug} onChange={(e) => setSlug(e.target.value)}>
            <option value="">—</option>
            {pages.map((p) => (
              <option key={p.slug} value={p.slug}>
                {p.title} ({p.slug})
              </option>
            ))}
          </select>
        </label>
      </Card>
      {view && (
        <Card title={view.title}>
          <p>{view.body}</p>
        </Card>
      )}
    </div>
  );
}
