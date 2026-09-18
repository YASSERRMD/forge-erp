import { useEffect, useState } from 'react';
import { BookMarked } from 'lucide-react';
import { apiExt, type Dictionary, type DictionaryEntry } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Dictionaries() {
  const { token } = useAuth();
  const { t, lang } = useLang();
  const [dicts, setDicts] = useState<Dictionary[]>([]);
  const [code, setCode] = useState('');
  const [entries, setEntries] = useState<DictionaryEntry[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    if (token) apiExt.dictionaries(token).then(setDicts).catch(() => undefined);
  }, [token]);

  useEffect(() => {
    if (token && code) {
      apiExt
        .dictionaryEntries(token, code, lang)
        .then(setEntries)
        .catch((e: Error) => setError(e.message));
    } else {
      setEntries([]);
    }
  }, [token, code, lang]);

  return (
    <div>
      <PageHeader icon={<BookMarked size={22} />} title={t('dictionaries')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('dictList')}>
        <label className="field">
          {t('dictionaries')}{' '}
          <select value={code} onChange={(e) => setCode(e.target.value)}>
            <option value="">—</option>
            {dicts.map((d) => (
              <option key={d.code} value={d.code}>
                {d.label} ({d.code})
              </option>
            ))}
          </select>
        </label>
        <p className="muted">
          {t('language')}: {lang}
        </p>
      </Card>
      {code && (
        <Card title={t('entries')}>
          <ul className="clean">
            {entries.map((e) => (
              <li key={e.code}>
                {e.label} <span className="muted">({e.code})</span>
              </li>
            ))}
          </ul>
          {entries.length === 0 && <p className="muted">{t('noData')}</p>}
        </Card>
      )}
    </div>
  );
}
