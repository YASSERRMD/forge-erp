import { useEffect, useState } from 'react';
import { api, type FinAccount } from '../api/client';
import { useAuth } from '../auth/AuthContext';

export function Finance() {
  const { token } = useAuth();
  const [accounts, setAccounts] = useState<FinAccount[]>([]);
  const [error, setError] = useState('');
  const [code, setCode] = useState('');
  const [label, setLabel] = useState('');
  const [type, setType] = useState('expense');
  const reload = () => {
    if (token) api.accounts(token).then(setAccounts).catch(() => undefined);
  };
  useEffect(reload, [token]);
  const create = () => {
    if (!token) return;
    api
      .createAccount(token, { code, label, type })
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };
  return (
    <section>
      <h2>Finance</h2>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      <h3>Chart of accounts</h3>
      <ul>
        {accounts.map((a) => (
          <li key={a.id}>
            {a.code} — {a.label} ({a.type})
          </li>
        ))}
      </ul>
      <h4>New account</h4>
      <label>
        Code <input value={code} onChange={(e) => setCode(e.target.value)} />
      </label>{' '}
      <label>
        Label <input value={label} onChange={(e) => setLabel(e.target.value)} />
      </label>{' '}
      <label>
        Type{' '}
        <select value={type} onChange={(e) => setType(e.target.value)}>
          <option value="asset">asset</option>
          <option value="liability">liability</option>
          <option value="equity">equity</option>
          <option value="revenue">revenue</option>
          <option value="expense">expense</option>
        </select>
      </label>{' '}
      <button onClick={create}>Create</button>
    </section>
  );
}
