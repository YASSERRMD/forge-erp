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
  body: string;
  status: number;
  row_version: number;
}

interface Asset {
  id: number;
  code: string;
  label: string;
  kind: string;
  serial: string;
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
  const [editId, setEditId] = useState<number | null>(null);
  const [acode, setAcode] = useState('');
  const [alabel, setAlabel] = useState('');
  const [aserial, setAserial] = useState('');
  const [editAsset, setEditAsset] = useState<number | null>(null);

  const reload = () => {
    if (!token) return;
    apiExt.articles(token, false).then((a) => setArticles(a as Article[])).catch(() => undefined);
    apiExt.assets(token).then((a) => setAssets(a as Asset[])).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>, after?: () => void) =>
    fn.then(() => {
      setError('');
      reload();
      after?.();
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
                {a.status === 0 && (
                  <button
                    onClick={() => {
                      setEditId(a.id);
                      setSlug(a.slug);
                      setTitle(a.title);
                      setBody(a.body);
                    }}
                  >
                    {t('edit')}
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
            onClick={() => {
              if (!token) return;
              if (editId !== null) {
                const row = articles.find((a) => a.id === editId);
                act(
                  apiExt.updateArticle(token, editId, {
                    title,
                    body,
                    tags: [],
                    row_version: row?.row_version ?? 0,
                  }),
                  () => setEditId(null),
                );
              } else {
                act(apiExt.createArticle(token, { slug, title, body }));
              }
            }}
          >
            {editId !== null ? t('save') : t('create')}
          </button>
          {editId !== null && (
            <button
              onClick={() => {
                setEditId(null);
                setSlug('');
                setTitle('');
                setBody('');
              }}
            >
              {t('cancel')}
            </button>
          )}
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
                {a.status !== 0 && (
                  <button
                    onClick={() => {
                      setEditAsset(a.id);
                      setAlabel(a.label);
                      setAserial(a.serial);
                    }}
                  >
                    {t('edit')}
                  </button>
                )}
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
          <label className="field">
            Serial <input value={aserial} onChange={(e) => setAserial(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => {
              if (!token) return;
              if (editAsset !== null) {
                const row = assets.find((a) => a.id === editAsset);
                act(
                  apiExt.updateAsset(token, editAsset, {
                    label: alabel,
                    serial: aserial,
                    row_version: row?.row_version ?? 0,
                  }),
                  () => setEditAsset(null),
                );
              } else {
                act(apiExt.createAsset(token, { code: acode, label: alabel, kind: 'equipment' }));
              }
            }}
          >
            {editAsset !== null ? t('save') : t('create')}
          </button>
        </Card>
      </div>
    </div>
  );
}
