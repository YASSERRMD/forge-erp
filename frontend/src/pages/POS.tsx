import { useState } from 'react';
import { api, type POSSale } from '../api/client';
import { useAuth } from '../auth/AuthContext';

export function POS() {
  const { token } = useAuth();
  const [sessionId, setSessionId] = useState('');
  const [sales, setSales] = useState<POSSale[]>([]);
  const [error, setError] = useState('');
  const load = () => {
    const id = Number(sessionId);
    if (!token || !id) return;
    api
      .sessionSales(token, id)
      .then((s) => {
        setSales(s);
        setError('');
      })
      .catch((e: Error) => setError(e.message));
  };
  return (
    <section>
      <h2>Point of sale</h2>
      <p>Checkout posts a validated invoice, full payment and stock moves.</p>
      <label>
        Session ID{' '}
        <input value={sessionId} onChange={(e) => setSessionId(e.target.value)} />
      </label>{' '}
      <button onClick={load}>Load sales</button>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      <ul>
        {sales.map((s) => (
          <li key={s.id}>
            {s.ref} — {(s.total_gross / 100).toFixed(2)} — change{' '}
            {(s.change / 100).toFixed(2)}
          </li>
        ))}
      </ul>
    </section>
  );
}
