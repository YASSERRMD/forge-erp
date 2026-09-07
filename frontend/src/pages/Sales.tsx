import { useEffect, useState } from 'react';
import { FileText } from 'lucide-react';
import { api, apiExt, type SalesDocument } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

const TYPES = ['proposal', 'order', 'invoice'] as const;

export function Sales() {
  const { token } = useAuth();
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
      <PageHeader icon={<FileText size={22} />} title="Sales" sub="Quotes, orders and shipments flow" />
      {error && <Alert>{error}</Alert>}
      <Card
        title="Commercial documents"
        action={
          <select value={type} onChange={(e) => setType(e.target.value)}>
            {TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        }
      >
        <ul className="clean">
          {docs.map((d) => (
            <li key={d.id}>
              <span>
                {d.ref} — {(d.totals.gross / 100).toFixed(2)}{' '}
                <Badge tone={statusTone(d.status)}>s{d.status}</Badge>
              </span>
              {d.status === 0 && token && (
                <button onClick={() => act(api.setDocumentStatus(token, d.id, 1))}>
                  Validate
                </button>
              )}
              {d.status === 1 && convertTarget && token && (
                <button onClick={() => act(apiExt.convertDoc(token, d.id, { to: convertTarget }))}>
                  To {convertTarget}
                </button>
              )}
            </li>
          ))}
        </ul>
        {docs.length === 0 && <p className="muted">No documents of this type.</p>}
        <h4>New {type} (sample line, org required)</h4>
        <label className="field">
          Org ID <input value={org} onChange={(e) => setOrg(e.target.value)} />
        </label>
        <button className="primary" onClick={create}>
          Create draft
        </button>
      </Card>
    </div>
  );
}
