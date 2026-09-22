import { useEffect, useState } from 'react';
import { MessagesSquare } from 'lucide-react';
import { apiExt, type Comment } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Collab() {
  const { token } = useAuth();
  const { t } = useLang();
  const [scope, setScope] = useState('sales');
  const [objectType, setObjectType] = useState('invoice');
  const [objectId, setObjectId] = useState('1');
  const [thread, setThread] = useState('general');
  const [body, setBody] = useState('');
  const [list, setList] = useState<Comment[]>([]);
  const [error, setError] = useState('');

  const reload = () => {
    if (token && scope && objectType && objectId) {
      apiExt
        .comments(token, scope, objectType, Number(objectId))
        .then(setList)
        .catch(() => setList([]));
    }
  };
  useEffect(reload, [token, scope, objectType, objectId]);

  const add = () => {
    if (!token || !body.trim()) return;
    apiExt
      .addComment(token, { scope, object_type: objectType, object_id: Number(objectId), thread, body })
      .then(() => {
        setError('');
        setBody('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  const remove = (id: number) => {
    if (!token) return;
    apiExt
      .removeComment(token, id)
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<MessagesSquare size={22} />} title={t('collab')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('threadBrowser')}>
        <label className="field">
          {t('scope')} <input value={scope} onChange={(e) => setScope(e.target.value)} />
        </label>
        <label className="field">
          {t('objectType')} <input value={objectType} onChange={(e) => setObjectType(e.target.value)} />
        </label>
        <label className="field">
          {t('objectId')} <input value={objectId} onChange={(e) => setObjectId(e.target.value)} />
        </label>
        <label className="field">
          {t('thread')} <input value={thread} onChange={(e) => setThread(e.target.value)} />
        </label>
        <ul className="clean">
          {list
            .filter((c) => !thread || c.thread === thread)
            .map((c) => (
              <li key={c.id}>
                <strong>{c.author}</strong> [{c.thread}] {c.body}{' '}
                <button onClick={() => remove(c.id)}>{t('delete')}</button>
              </li>
            ))}
        </ul>
        {list.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('value')}{' '}
          <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={2} style={{ width: '100%' }} />
        </label>
        <button className="primary" onClick={add}>
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
