import { useState } from 'react';
import { api, type CheckoutLine, type POSSale } from '../api/client';
import { useAuth } from '../auth/AuthContext';

export function POS() {
  const { token } = useAuth();
  const [sessionId, setSessionId] = useState('');
  const [sales, setSales] = useState<POSSale[]>([]);
  const [error, setError] = useState('');
  const [orgId, setOrgId] = useState('');
  const [method, setMethod] = useState('cash');
  const [tendered, setTendered] = useState('');
  const [lines, setLines] = useState<CheckoutLine[]>([{ product_id: 0, qty: 1 }]);
  const [result, setResult] = useState('');

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

  const setLine = (i: number, patch: Partial<CheckoutLine>) =>
    setLines((ls) => ls.map((l, j) => (j === i ? { ...l, ...patch } : l)));

  const checkout = () => {
    if (!token) return;
    api
      .checkout(token, {
        session_id: Number(sessionId),
        org_id: Number(orgId),
        lines,
        method,
        tendered: Math.round(Number(tendered) * 100),
      })
      .then((r) => {
        setResult(`Sale ${r.ref}: gross ${(r.total_gross / 100).toFixed(2)}, change ${(r.change / 100).toFixed(2)}`);
        setError('');
        load();
      })
      .catch((e: Error) => {
        setError(e.message);
        setResult('');
      });
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
      <h3>Checkout</h3>
      <label>
        Customer org ID{' '}
        <input value={orgId} onChange={(e) => setOrgId(e.target.value)} />
      </label>{' '}
      <label>
        Method{' '}
        <select value={method} onChange={(e) => setMethod(e.target.value)}>
          <option value="cash">cash</option>
          <option value="card">card</option>
          <option value="transfer">transfer</option>
        </select>
      </label>{' '}
      <label>
        Tendered{' '}
        <input value={tendered} onChange={(e) => setTendered(e.target.value)} />
      </label>
      {lines.map((l, i) => (
        <div key={i}>
          <label>
            Product{' '}
            <input
              type="number"
              value={l.product_id || ''}
              onChange={(e) => setLine(i, { product_id: Number(e.target.value) })}
            />
          </label>{' '}
          <label>
            Qty{' '}
            <input
              type="number"
              value={l.qty}
              onChange={(e) => setLine(i, { qty: Number(e.target.value) })}
            />
          </label>
        </div>
      ))}
      <button onClick={() => setLines((ls) => [...ls, { product_id: 0, qty: 1 }])}>
        Add line
      </button>{' '}
      <button onClick={checkout}>Checkout</button>
      {result && <p>{result}</p>}
    </section>
  );
}
