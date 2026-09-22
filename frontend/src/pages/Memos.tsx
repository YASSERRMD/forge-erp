import { useEffect, useState } from 'react';
import { NotebookPen } from 'lucide-react';
import { apiExt, type Memo } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Memos() {
  const { token } = useAuth();
  const { t } = useLang();
  const [memos, setMemos] = useState<Memo[]>([]);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [editing, setEditing] = useState<Memo | null>(null);
  const [error, setError] = useState('');

  const reload = () => {
    if (token) apiExt.memos(token).then(setMemos).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const save = () => {
    if (!token) return;
    const done = (m: Memo) => {
      setError('');
      setTitle('');
      setBody('');
      setEditing(null);
      reload();
      return m;
    };
    if (editing) {
      apiExt
        .updateMemo(token, editing.id, { title, body, row_version: editing.row_version })
        .then(done)
        .catch((e: Error) => setError(e.message));
    } else {
      apiExt
        .createMemo(token, { title, body })
        .then(done)
        .catch((e: Error) => setError(e.message));
    }
  };

  const remove = (id: number) => {
    if (!token) return;
    apiExt
      .deleteMemo(token, id)
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<NotebookPen size={22} />} title={t('memos')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('memoList')}>
        <ul className="clean">
          {memos.map((m) => (
            <li key={m.id}>
              <strong>{m.title}</strong> <span className="muted">{m.body.slice(0, 80)}</span>{' '}
              <button
                onClick={() => {
                  setEditing(m);
                  setTitle(m.title);
                  setBody(m.body);
                }}
              >
                {t('edit')}
              </button>{' '}
              <button onClick={() => remove(m.id)}>{t('delete')}</button>
            </li>
          ))}
        </ul>
        {memos.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={editing ? t('editMemo') : t('newMemo')}>
        <label className="field">
          {t('label')} <input value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="field">
          {t('value')}{' '}
          <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={3} style={{ width: '100%' }} />
        </label>
        <button className="primary" onClick={save}>
          {t('save')}
        </button>{' '}
        {editing && (
          <button
            onClick={() => {
              setEditing(null);
              setTitle('');
              setBody('');
            }}
          >
            {t('cancel')}
          </button>
        )}
      </Card>
    </div>
  );
}
