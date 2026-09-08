import { useEffect, useState } from 'react';
import { Building2, Package, Receipt } from 'lucide-react';
import { api, type Organization, type Product, type SalesDocument } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

function useFetch<T>(fn: (token: string) => Promise<T>, fallback: T): T {
  const { token } = useAuth();
  const [data, setData] = useState<T>(fallback);
  useEffect(() => {
    if (token) fn(token).then(setData).catch(() => undefined);
  }, [token]);
  return data;
}

export function Organizations() {
  const { token } = useAuth();
  const { t } = useLang();
  const orgs = useFetch<Organization[]>((x) => api.organizations(x), []);
  const [name, setName] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const create = () => {
    if (!token) return;
    api
      .createOrganization(token, { name, is_customer: true, customer_code: code })
      .then(() => window.location.reload())
      .catch((e: Error) => setError(e.message));
  };
  return (
    <div>
      <PageHeader icon={<Building2 size={22} />} title={t('organizations')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('customer')}>
        <ul className="clean">
          {orgs.map((o) => (
            <li key={o.id}>
              <span>
                {o.name} {o.customer_code && `(${o.customer_code})`}
              </span>
            </li>
          ))}
        </ul>
        {orgs.length === 0 && <p className="muted">{t('noData')}</p>}
        <h4>{t('newCustomer')}</h4>
        <label className="field">
          {t('name')} <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
        <button className="primary" onClick={create}>
          {t('create')}
        </button>
      </Card>
    </div>
  );
}

export function Products() {
  const { token } = useAuth();
  const { t } = useLang();
  const prods = useFetch<Product[]>((x) => api.products(x), []);
  const [sku, setSku] = useState('');
  const [name, setName] = useState('');
  const [price, setPrice] = useState('');
  const [error, setError] = useState('');
  const create = () => {
    if (!token) return;
    api
      .createProduct(token, {
        sku,
        name,
        type: 0,
        unit: 'unit',
        net_price: Math.round(Number(price) * 100),
        vat_rate_bps: 2000,
        stock_tracked: true,
      })
      .then(() => window.location.reload())
      .catch((e: Error) => setError(e.message));
  };
  return (
    <div>
      <PageHeader icon={<Package size={22} />} title={t('products')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('products')}>
        <ul className="clean">
          {prods.map((p) => (
            <li key={p.id}>
              <span>
                {p.sku} — {p.name} ({money(p.net_price)})
              </span>
            </li>
          ))}
        </ul>
        {prods.length === 0 && <p className="muted">{t('noData')}</p>}
        <h4>{t('newProduct')}</h4>
        <label className="field">
          {t('sku')} <input value={sku} onChange={(e) => setSku(e.target.value)} />
        </label>
        <label className="field">
          {t('name')} <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="field">
          {t('price')} <input value={price} onChange={(e) => setPrice(e.target.value)} />
        </label>
        <button className="primary" onClick={create}>
          {t('create')}
        </button>
      </Card>
    </div>
  );
}

const invStatus = (s: number, t: (k: 'stDraft' | 'stValidated' | 'stPartiallyPaid' | 'stPaid') => string) =>
  s === 0 ? t('stDraft') : s === 1 ? t('stValidated') : s === 2 ? t('stPartiallyPaid') : t('stPaid');

export function Invoices() {
  const { token } = useAuth();
  const { t } = useLang();
  const invs = useFetch<SalesDocument[]>((x) => api.invoices(x), []);
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
    <div>
      <PageHeader icon={<Receipt size={22} />} title={t('invoices')} />
      {notice && <Alert>{notice}</Alert>}
      <Card title={t('invoices')}>
        <ul className="clean">
          {invs.map((d) => (
            <li key={d.id}>
              <span>
                {d.ref} <Badge tone={statusTone(d.status)}>{invStatus(d.status, t)}</Badge> {money(d.totals.gross)}
              </span>
              {d.status === 0 && <button onClick={() => validate(d.id)}>{t('validate')}</button>}
              {(d.status === 1 || d.status === 0) && (
                <button className="primary" onClick={() => payFull(d.id)}>
                  {t('pay')}
                </button>
              )}
            </li>
          ))}
        </ul>
        {invs.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
