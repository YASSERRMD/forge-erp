import { useState } from 'react';
import { Search as SearchIcon } from 'lucide-react';
import { apiExt, type SearchHit } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

export function Search() {
  const { token } = useAuth();
  const { t } = useLang();
  const [q, setQ] = useState('');
  const [scope, setScope] = useState('');
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [error, setError] = useState('');

  const run = () => {
    if (!token || !q.trim()) return;
    apiExt
      .searchAll(token, q, scope)
      .then((r) => {
        setHits(r);
        setError('');
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<SearchIcon size={22} />} title={t('globalSearch')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('globalSearch')}>
        <label className="field">
          {t('prompt')}{' '}
          <input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} />
        </label>
        <label className="field">
          {t('scope')}{' '}
          <input value={scope} onChange={(e) => setScope(e.target.value)} placeholder={t('scopeHint')} />
        </label>
        <button className="primary" onClick={run}>
          {t('ask')}
        </button>
        <ul className="clean">
          {hits.map((h, i) => (
            <li key={`${h.scope}-${h.id}-${i}`}>
              <Badge>{h.scope}</Badge> {h.label} <span className="muted">({h.ref})</span>
            </li>
          ))}
        </ul>
        {hits.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
