import { useEffect, useState } from 'react';
import { BookOpen } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

interface Article {
  id: number;
  slug: string;
  title: string;
  status: number;
  row_version: number;
}

interface Asset {
  id: number;
  code: string;
  label: string;
  kind: string;
  status: number;
  row_version: number;
}

export function Knowledge() {
  const { token } = useAuth();
  const { t } = useLang();
  const [articles, setArticles] = useState<Article[]>([]);
  const [assets, setAssets] = useState<Asset[]>([]);
  const [error, setError] = useState('');
  const [q, setQ] = useState('');
  const [hits, setHits] = useState<Article[]>([]);
  const [slug, setSlug] = useState('');
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [acode, setAcode] = useState('');
  const [alabel, setAlabel] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.articles(token, false).then((a) => setArticles(a as Article[])).catch(() => undefined);
    apiExt.assets(token).then((a) => setAssets(a as Asset[])).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  const search = () => {
    if (!token) return;
    apiExt.kbSearch(token, q).then((h) => setHits(h as Article[])).catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<BookOpen size={22} />} title={t('knowledge')} />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title={t('knowledge')}>
          <ul className="clean">
            {articles.map((a) => (
              <li key={a.id}>
                <span>
                  {a.slug} — {a.title}
                </span>
                <Badge tone={a.status === 1 ? 'ok' : 'warn'}>{a.status === 1 ? t('stPublished') : t('stDraft')}</Badge>
                {a.status === 0 && token && (
                  <button onClick={() => act(apiExt.setArticleStatus(token, a.id, { status: 1, row_version: a.row_version }))}>
                    {t('publish')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {articles.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newArticle')}</h4>
          <label className="field">
            {t('slug')} <input value={slug} onChange={(e) => setSlug(e.target.value)} />
          </label>
          <label className="field">
            {t('title')} <input value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <label className="field">
            {t('body')} <input value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(apiExt.createArticle(token, { slug, title, body }))}
          >
            {t('create')}
          </button>
          <h4>{t('search')}</h4>
          <label className="field">
            {t('search')} <input value={q} onChange={(e) => setQ(e.target.value)} />
          </label>
          <button onClick={search}>{t('search')}</button>
          <ul className="clean">
            {hits.map((h) => (
              <li key={h.id}>
                <span>
                  {h.slug} — {h.title}
                </span>
              </li>
            ))}
          </ul>
        </Card>
        <Card title={t('knowledge')}>
          <ul className="clean">
            {assets.map((a) => (
              <li key={a.id}>
                <span>
                  {a.code} — {a.label} [{a.kind}]
                </span>
                <Badge tone={a.status === 1 ? 'ok' : a.status === 2 ? 'warn' : 'bad'}>
                  {a.status === 1 ? t('stInService') : a.status === 2 ? t('stMaintenance') : t('stRetired')}
                </Badge>
              </li>
            ))}
          </ul>
          {assets.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newAsset')}</h4>
          <label className="field">
            {t('code')} <input value={acode} onChange={(e) => setAcode(e.target.value)} />
          </label>
          <label className="field">
            {t('label')} <input value={alabel} onChange={(e) => setAlabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(apiExt.createAsset(token, { code: acode, label: alabel, kind: 'equipment' }))}
          >
            {t('create')}
          </button>
        </Card>
      </div>
    </div>
  );
}
