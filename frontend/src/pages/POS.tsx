import { useState } from 'react';
import { ShoppingCart } from 'lucide-react';
import { api, apiExt, type CheckoutLine, type POSSale } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

export function POS() {
  const { token } = useAuth();
  const { t } = useLang();
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
        tendered: tenders.reduce((s, x) => s + Math.round(Number(x.amount) * 100), 0),
        payments: tenders.map((x) => ({ method: x.method, amount: Math.round(Number(x.amount) * 100) })),
      })
      .then((r) => {
        setResult(`Sale ${r.ref}: ${t('gross')} ${money(r.total_gross)}, ${t('balance')} ${money(r.change)}`);
        setError('');
        load();
      })
      .catch((e: Error) => {
        setError(e.message);
        setResult('');
      });
  };

  return (
    <div>
      <PageHeader icon={<ShoppingCart size={22} />} title={t('pos')} />
      <label className="field">
        {t('session')} ID <input value={sessionId} onChange={(e) => setSessionId(e.target.value)} />
      </label>
      <button onClick={load}>{t('load')}</button>
      {error && <Alert>{error}</Alert>}
      <Card title={t('pos')}>
        <ul className="clean">
          {sales.map((s) => (
            <li key={s.id}>
              <span>
                {s.ref} — {money(s.total_gross)} — {t('balance')} {money(s.change)}{' '}
                <Badge tone={statusTone(s.status)}>
                  {s.status === 1 ? t('stPaid') : s.status === 2 ? t('stDone') : t('stCanceled')}
                </Badge>
              </span>
              {s.status === 1 && <button onClick={() => returnSale(s.id)}>Return</button>}
            </li>
          ))}
        </ul>
      </Card>
      <Card title={t('checkout')}>
        <label className="field">
          {t('customer')} {t('orgId')}{' '}
          <input value={orgId} onChange={(e) => setOrgId(e.target.value)} />
        </label>
        {tenders.map((x, i) => (
          <div key={i}>
            <label className="field">
              {t('method')}{' '}
              <select
                value={x.method}
                onChange={(e) =>
                  setTenders((ts) => ts.map((y, j) => (j === i ? { ...y, method: e.target.value } : y)))
                }
              >
                <option value="cash">{t('cash')}</option>
                <option value="card">{t('card')}</option>
                <option value="transfer">{t('transfer')}</option>
              </select>
            </label>
            <label className="field">
              {t('amount')}{' '}
              <input
                value={x.amount}
                onChange={(e) =>
                  setTenders((ts) => ts.map((y, j) => (j === i ? { ...y, amount: e.target.value } : y)))
                }
              />
            </label>
          </div>
        ))}
        <button onClick={() => setTenders((ts) => [...ts, { method: 'cash', amount: '' }])}>
          {t('add')} tender
        </button>
        {lines.map((l, i) => (
          <div key={i}>
            <label className="field">
              {t('product')}{' '}
              <input
                type="number"
                value={l.product_id || ''}
                onChange={(e) => setLine(i, { product_id: Number(e.target.value) })}
              />
            </label>
            <label className="field">
              {t('qty')}{' '}
              <input
                type="number"
                value={l.qty}
                onChange={(e) => setLine(i, { qty: Number(e.target.value) })}
              />
            </label>
          </div>
        ))}
        <button onClick={() => setLines((ls) => [...ls, { product_id: 0, qty: 1 }])}>
          {t('add')} line
        </button>{' '}
        <button className="primary" onClick={checkout}>
          {t('checkout')}
        </button>
        {result && <p className="alert-ok">{result}</p>}
      </Card>
    </div>
  );
}
