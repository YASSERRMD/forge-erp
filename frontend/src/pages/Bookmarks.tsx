import { useEffect, useState } from 'react';
import { Bookmark as BookmarkIcon } from 'lucide-react';
import { apiExt, type Bookmark } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Bookmarks() {
  const { token } = useAuth();
  const { t } = useLang();
  const [list, setList] = useState<Bookmark[]>([]);
  const [scope, setScope] = useState('');
  const [objectType, setObjectType] = useState('');
  const [objectId, setObjectId] = useState('');
  const [error, setError] = useState('');

  const reload = () => {
    if (token) apiExt.bookmarks(token).then(setList).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const add = () => {
    if (!token) return;
    apiExt
      .addBookmark(token, { scope, object_type: objectType, object_id: Number(objectId) })
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  const remove = (id: number) => {
    if (!token) return;
    apiExt
      .removeBookmark(token, id)
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<BookmarkIcon size={22} />} title={t('bookmarks')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('bookmarkList')}>
        <ul className="clean">
          {list.map((b) => (
            <li key={b.id}>
              {b.scope}/{b.object_type}#{b.object_id}{' '}
              <span className="muted">({b.user_login})</span>{' '}
              <button onClick={() => remove(b.id)}>{t('delete')}</button>
            </li>
          ))}
        </ul>
        {list.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('newBookmark')}>
        <label className="field">
          {t('scope')} <input value={scope} onChange={(e) => setScope(e.target.value)} placeholder="sales" />
        </label>
        <label className="field">
          {t('objectType')}{' '}
          <input value={objectType} onChange={(e) => setObjectType(e.target.value)} placeholder="invoice" />
        </label>
        <label className="field">
          {t('objectId')} <input value={objectId} onChange={(e) => setObjectId(e.target.value)} />
        </label>
        <button className="primary" onClick={add}>
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
