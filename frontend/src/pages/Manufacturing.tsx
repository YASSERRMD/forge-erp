import { useEffect, useState } from 'react';
import { Factory } from 'lucide-react';
import { api, apiExt, type BOM, type ManufacturingOrder } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

const moStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Validated',
  2: 'In progress',
  3: 'Produced',
  [-1]: 'Canceled',
};

export function Manufacturing() {
  const { token } = useAuth();
  const [boms, setBoms] = useState<BOM[]>([]);
  const [mos, setMos] = useState<ManufacturingOrder[]>([]);
  const [error, setError] = useState('');
  const [bref, setBref] = useState('');
  const [bproduct, setBproduct] = useState('');
  const [blabel, setBlabel] = useState('');
  const [mref, setMref] = useState('');
  const [mbom, setMbom] = useState('');
  const [mproduct, setMproduct] = useState('');
  const [mwh, setMwh] = useState('');
  const [mqty, setMqty] = useState('');

  const reload = () => {
    if (!token) return;
    api.boms(token).then(setBoms).catch(() => undefined);
    api.mos(token).then(setMos).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  const advance = (m: ManufacturingOrder) => {
    if (!token) return;
    const next = m.status === 0 ? 1 : m.status === 1 ? 2 : null;
    if (next === null) return;
    act(apiExt.setMOStatus(token, m.id, { status: next, row_version: m.row_version }));
  };

  return (
    <div>
      <PageHeader icon={<Factory size={22} />} title="Manufacturing" sub="Bills of materials and production runs" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Bills of materials">
          <ul className="clean">
            {boms.map((b) => (
              <li key={b.id}>
                <span>
                  {b.ref} — {b.label}
                </span>
              </li>
            ))}
          </ul>
          <h4>New BOM</h4>
          <label className="field">
            Ref <input value={bref} onChange={(e) => setBref(e.target.value)} />
          </label>
          <label className="field">
            Product ID <input value={bproduct} onChange={(e) => setBproduct(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={blabel} onChange={(e) => setBlabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(
                apiExt.createBOM(token, {
                  ref: bref,
                  product_id: Number(bproduct),
                  label: blabel,
                }),
              )
            }
          >
            Create
          </button>
        </Card>
        <Card title="Manufacturing orders">
          <ul className="clean">
            {mos.map((m) => (
              <li key={m.id}>
                <span>
                  {m.ref} — qty {m.qty} <Badge tone={statusTone(m.status)}>{moStatus[m.status] ?? m.status}</Badge>
                </span>
                {(m.status === 0 || m.status === 1) && (
                  <button onClick={() => advance(m)}>{m.status === 0 ? 'Validate' : 'Start'}</button>
                )}
                {m.status === 2 && token && (
                  <button className="primary" onClick={() => act(api.produceMO(token, m.id))}>
                    Produce
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New MO</h4>
          <label className="field">
            Ref <input value={mref} onChange={(e) => setMref(e.target.value)} />
          </label>
          <label className="field">
            BOM ID <input value={mbom} onChange={(e) => setMbom(e.target.value)} />
          </label>
          <label className="field">
            Product ID <input value={mproduct} onChange={(e) => setMproduct(e.target.value)} />
          </label>
          <label className="field">
            Warehouse ID <input value={mwh} onChange={(e) => setMwh(e.target.value)} />
          </label>
          <label className="field">
            Qty <input value={mqty} onChange={(e) => setMqty(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(
                apiExt.createMO(token, {
                  ref: mref,
                  bom_id: Number(mbom),
                  product_id: Number(mproduct),
                  warehouse_id: Number(mwh),
                  qty: Number(mqty),
                }),
              )
            }
          >
            Create
          </button>
        </Card>
      </div>
    </div>
  );
}
