import { useEffect, useState } from 'react';
import { FileText } from 'lucide-react';
import { api, apiExt, type SalesDocument } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

const TYPES = ['proposal', 'order', 'invoice'] as const;

export function Sales() {
  const { token } = useAuth();
  const { t } = useLang();
  const [type, setType] = useState<string>('proposal');
  const [docs, setDocs] = useState<SalesDocument[]>([]);
  const [error, setError] = useState('');
  const [org, setOrg] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.salesDocs(token, type).then((d) => setDocs(d as SalesDocument[])).catch((e: Error) => setError(e.message));
  };
  useEffect(reload, [token, type]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  const create = () => {
    if (!token) return;
    act(
      apiExt.createSalesDoc(token, {
        type,
        org_id: Number(org),
        currency: 'USD',
        lines: [{ product_id: 1, label: 'Sample line', qty: 1, unit_net: 1000, vat_rate_bps: 2000 }],
      }),
    );
  };

  const convertTarget = type === 'proposal' ? 'order' : type === 'order' ? 'invoice' : null;

  return (
    <div>
      <PageHeader icon={<FileText size={22} />} title={t('sales')} />
      {error && <Alert>{error}</Alert>}
      <Card
        title={t('sales')}
        action={
          <select value={type} onChange={(e) => setType(e.target.value)}>
            {TYPES.map((x) => (
              <option key={x} value={x}>
                {x}
              </option>
            ))}
          </select>
        }
      >
        <ul className="clean">
          {docs.map((d) => (
            <li key={d.id}>
              <span>
                {d.ref} — {money(d.totals.gross)}{' '}
                <Badge tone={statusTone(d.status)}>s{d.status}</Badge>
              </span>
              {d.status === 0 && token && (
                <button onClick={() => act(api.setDocumentStatus(token, d.id, 1))}>
                  {t('validate')}
                </button>
              )}
              {d.status === 1 && convertTarget && token && (
                <button onClick={() => act(apiExt.convertDoc(token, d.id, { to: convertTarget }))}>
                  → {convertTarget}
                </button>
              )}
            </li>
          ))}
        </ul>
        {docs.length === 0 && <p className="muted">{t('noData')}</p>}
        <h4>{t('create')} {type}</h4>
        <label className="field">
          {t('orgId')} <input value={org} onChange={(e) => setOrg(e.target.value)} />
        </label>
        <button className="primary" onClick={create}>
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
