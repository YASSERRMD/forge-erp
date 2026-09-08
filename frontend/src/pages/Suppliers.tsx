import { useEffect, useState } from 'react';
import { Truck } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

interface PurchaseDoc {
  id: number;
  ref: string;
  type: string;
  status: number;
}

export function Suppliers() {
  const { token } = useAuth();
  const { t } = useLang();
  const [docs, setDocs] = useState<PurchaseDoc[]>([]);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('supplier_order');

  const reload = () => {
    if (!token) return;
    apiExt
      .purchaseDocs(token, filter)
      .then((d) => setDocs(d as PurchaseDoc[]))
      .catch((e: Error) => setError(e.message));
  };
  useEffect(reload, [token, filter]);

  const approve = (id: number) => {
    if (!token) return;
    apiExt
      .approvePurchase(token, id, {})
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<Truck size={22} />} title={t('suppliers')} />
      {error && <Alert>{error}</Alert>}
      <Card
        title={t('suppliers')}
        action={
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="supplier_order">{t('orders')}</option>
            <option value="supplier_proposal">{t('priceRequests')}</option>
            <option value="supplier_invoice">{t('invoices')}</option>
            <option value="reception">{t('receptions')}</option>
          </select>
        }
      >
        <ul className="clean">
          {docs.map((d) => (
            <li key={d.id}>
              <span>
                {d.ref} — {d.type}
              </span>
              <Badge tone={statusTone(d.status)}>s{d.status}</Badge>
              {d.status === 0 && (
                <button onClick={() => approve(d.id)}>{t('approve')}</button>
              )}
            </li>
          ))}
        </ul>
        {docs.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
