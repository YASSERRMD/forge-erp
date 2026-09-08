import { useEffect, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

interface UserRow {
  id: number;
  login: string;
  email: string;
  is_admin: boolean;
  status: number;
  row_version: number;
}

export function Admin() {
  const { token } = useAuth();
  const { t } = useLang();
  const [users, setUsers] = useState<UserRow[]>([]);
  const [error, setError] = useState('');
  const [login, setLogin] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  const reload = () => {
    if (token) apiExt.usersList(token).then((u) => setUsers(u as UserRow[])).catch(() => undefined);
  };
  useEffect(reload, [token]);

  return (
    <div>
      <PageHeader icon={<ShieldCheck size={22} />} title={t('admin')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('user')}>
        <ul className="clean">
          {users.map((u) => (
            <li key={u.id}>
              <span>
                {u.login} — {u.email} {u.is_admin && <strong>(admin)</strong>}{' '}
                <Badge tone={u.status === 1 ? 'ok' : 'bad'}>{u.status === 1 ? t('stActive') : t('stInactive')}</Badge>
              </span>
              {u.status === 1 && (
                <button
                  onClick={() =>
                    apiExt
                      .updateUser(token ?? '', u.id, { status: 0, row_version: u.row_version })
                      .then(() => reload())
                      .catch((e: Error) => setError(e.message))
                  }
                >
                  {t('close')}
                </button>
              )}
            </li>
          ))}
        </ul>
        {users.length === 0 && <p className="muted">{t('noData')}</p>}
        <h4>{t('newUser')}</h4>
        <label className="field">
          {t('login')} <input value={login} onChange={(e) => setLogin(e.target.value)} />
        </label>
        <label className="field">
          {t('email')} <input value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label className="field">
          {t('password')} <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            apiExt
              .createUser(token, { login, email, password })
              .then(() => {
                setError('');
                reload();
              })
              .catch((e: Error) => setError(e.message))
          }
        >
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
