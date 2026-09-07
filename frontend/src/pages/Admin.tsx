import { useEffect, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Card, PageHeader } from '../components/ui';

interface UserRow {
  id: number;
  login: string;
  email: string;
  is_admin: boolean;
}

export function Admin() {
  const { token } = useAuth();
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
      <PageHeader icon={<ShieldCheck size={22} />} title="Administration" sub="Users and access" />
      {error && <Alert>{error}</Alert>}
      <Card title="Users">
        <ul className="clean">
          {users.map((u) => (
            <li key={u.id}>
              <span>
                {u.login} — {u.email} {u.is_admin && <strong>(admin)</strong>}
              </span>
            </li>
          ))}
        </ul>
        <h4>New user (min 10-char password)</h4>
        <label className="field">
          Login <input value={login} onChange={(e) => setLogin(e.target.value)} />
        </label>
        <label className="field">
          Email <input value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label className="field">
          Password <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
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
          Create
        </button>
      </Card>
    </div>
  );
}
