import { useEffect, useState } from 'react';
import { Languages } from 'lucide-react';
import { apiExt, type LocaleInfo } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Locales() {
  const { token } = useAuth();
  const { t } = useLang();
  const [list, setList] = useState<LocaleInfo[]>([]);
  const [code, setCode] = useState('');
  const [detail, setDetail] = useState<(LocaleInfo & { strings?: Record<string, string> }) | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (token) apiExt.locales(token).then(setList).catch(() => undefined);
  }, [token]);

  useEffect(() => {
    if (token && code) {
      apiExt
        .localeDetail(token, code)
        .then((d) => {
          setDetail(d);
          setError('');
        })
        .catch((e: Error) => setError(e.message));
    } else {
      setDetail(null);
    }
  }, [token, code]);

  return (
    <div>
      <PageHeader icon={<Languages size={22} />} title={t('localeCatalogue')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('catalogueList')}>
        <label className="field">
          {t('localeCatalogue')}{' '}
          <select value={code} onChange={(e) => setCode(e.target.value)}>
            <option value="">—</option>
            {list.map((l) => (
              <option key={l.code} value={l.code}>
                {l.code} ({l.keys} {t('lines')})
              </option>
            ))}
          </select>
        </label>
        <ul className="clean">
          {list.map((l) => (
            <li key={l.code}>
              {l.code} <span className="muted">{l.direction} · {l.decimal}/{l.thousand} · {l.keys} {t('lines')}</span>
            </li>
          ))}
        </ul>
        {list.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      {detail && (
        <Card title={`${t('localeCatalogue')}: ${detail.code}`}>
          <p className="muted">
            {detail.direction} · {t('pluralRule')}: {detail.plural} · {detail.decimal}/{detail.thousand} ·{' '}
            {detail.keys} {t('lines')}
          </p>
        </Card>
      )}
    </div>
  );
}
