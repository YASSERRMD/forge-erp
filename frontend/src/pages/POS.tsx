import { useState } from 'react';
import { api, apiExt, type CheckoutLine, type POSSale } from '../api/client';
import { useAuth } from '../auth/AuthContext';

export function POS() {
  const { token } = useAuth();
  const [sessionId, setSessionId] = useState('');
  const [sales, setSales] = useState<POSSale[]>([]);
  const [error, setError] = useState('');
  const [orgId, setOrgId] = useState('');
  const [tenders, setTenders] = useState<Array<{ method: string; amount: string }>>([
    { method: 'cash', amount: '' },
  ]);
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

  const returnSale = (id: number) => {
    if (!token) return;
    apiExt
      .returnSale(token, id)
      .then(() => {
        setError('');
        setResult('Returned with credit note');
        load();
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
        method: tenders.length === 1 ? tenders[0].method : 'mixed',
        tendered: tenders.reduce((s, t) => s + Math.round(Number(t.amount) * 100), 0),
        payments: tenders.map((t) => ({ method: t.method, amount: Math.round(Number(t.amount) * 100) })),
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
            {s.status === 1 && (
              <button onClick={() => returnSale(s.id)}>Return</button>
            )}
          </li>
        ))}
      </ul>
      <h3>Checkout</h3>
      <label>
        Customer org ID{' '}
        <input value={orgId} onChange={(e) => setOrgId(e.target.value)} />
      </label>
      {tenders.map((t, i) => (
        <div key={i}>
          <label>
            Method{' '}
            <select
              value={t.method}
              onChange={(e) =>
                setTenders((ts) => ts.map((x, j) => (j === i ? { ...x, method: e.target.value } : x)))
              }
            >
              <option value="cash">cash</option>
              <option value="card">card</option>
              <option value="transfer">transfer</option>
            </select>
          </label>{' '}
          <label>
            Amount{' '}
            <input
              value={t.amount}
              onChange={(e) =>
                setTenders((ts) => ts.map((x, j) => (j === i ? { ...x, amount: e.target.value } : x)))
              }
            />
          </label>
        </div>
      ))}
      <button onClick={() => setTenders((ts) => [...ts, { method: 'cash', amount: '' }])}>
        Add tender
      </button>
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
