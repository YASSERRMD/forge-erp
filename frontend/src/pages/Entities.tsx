import { useEffect, useState } from 'react';
import { api, type Organization, type Product, type SalesDocument } from '../api/client';
import { useAuth } from '../auth/AuthContext';

function useFetch<T>(fn: (token: string) => Promise<T>, fallback: T): T {
  const { token } = useAuth();
  const [data, setData] = useState<T>(fallback);
  useEffect(() => {
    if (token) fn(token).then(setData).catch(() => undefined);
  }, [token]);
  return data;
}

export function Dashboard() {
  const { login } = useAuth();
  return (
    <section>
      <h2>Dashboard</h2>
      <p>Signed in as {login}. KPIs stream from reporting endpoints (Phase 10).</p>
    </section>
  );
}

export function Organizations() {
  const orgs = useFetch<Organization[]>((t) => api.organizations(t), []);
  return (
    <section>
      <h2>Organizations</h2>
      <ul>
        {orgs.map((o) => (
          <li key={o.id}>
            {o.name} {o.customer_code && `(${o.customer_code})`}
          </li>
        ))}
      </ul>
    </section>
  );
}

export function Products() {
  const prods = useFetch<Product[]>((t) => api.products(t), []);
  return (
    <section>
      <h2>Products</h2>
      <ul>
        {prods.map((p) => (
          <li key={p.id}>
            {p.sku} — {p.name} ({(p.net_price / 100).toFixed(2)})
          </li>
        ))}
      </ul>
    </section>
  );
}

export function Invoices() {
  const { token } = useAuth();
  const invs = useFetch<SalesDocument[]>((t) => api.invoices(t), []);
  const [notice, setNotice] = useState('');
  const refresh = () => {
    if (token) api.invoices(token).then(() => window.location.reload()).catch(() => undefined);
  };
  const validate = (id: number) => {
    if (!token) return;
    api
      .setDocumentStatus(token, id, 1)
      .then(() => refresh())
      .catch((e: Error) => setNotice(e.message));
  };
  const payFull = async (id: number) => {
    if (!token) return;
    try {
      const doc = await api.getDocument(token, id);
      await api.payInvoice(token, {
        org_id: doc.org_id,
        amount: doc.totals.gross,
        currency: 'USD',
        method: 'transfer',
        invoice_ids: [id],
      });
      refresh();
    } catch (e) {
      setNotice((e as Error).message);
    }
  };
  return (
    <section>
      <h2>Invoices</h2>
      {notice && <p style={{ color: 'red' }}>{notice}</p>}
      <ul>
        {invs.map((d) => (
          <li key={d.id}>
            {d.ref} — status {d.status} — {(d.totals.gross / 100).toFixed(2)}{' '}
            {d.status === 0 && <button onClick={() => validate(d.id)}>Validate</button>}{' '}
            {(d.status === 1 || d.status === 0) && (
              <button onClick={() => payFull(d.id)}>Pay full</button>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
