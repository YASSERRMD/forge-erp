import { useEffect, useState } from 'react';
import { Factory } from 'lucide-react';
import { api, apiExt, type BOM, type ManufacturingOrder } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

export function Manufacturing() {
  const { token } = useAuth();
  const { t } = useLang();
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

  const moName = (s: number) =>
    s === 0 ? t('stDraft') : s === 1 ? t('stValidated') : s === 2 ? t('stInProgress') : s === 3 ? t('stProduced') : t('stCanceled');

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
      <PageHeader icon={<Factory size={22} />} title={t('manufacturing')} />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="BOM">
          <ul className="clean">
            {boms.map((b) => (
              <li key={b.id}>
                <span>
                  {b.ref} — {b.label}
                </span>
              </li>
            ))}
          </ul>
          {boms.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newBOM')}</h4>
          <label className="field">
            {t('ref')} <input value={bref} onChange={(e) => setBref(e.target.value)} />
          </label>
          <label className="field">
            {t('productId')} <input value={bproduct} onChange={(e) => setBproduct(e.target.value)} />
          </label>
          <label className="field">
            {t('label')} <input value={blabel} onChange={(e) => setBlabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(apiExt.createBOM(token, { ref: bref, product_id: Number(bproduct), label: blabel }))
            }
          >
            {t('create')}
          </button>
        </Card>
        <Card title="MO">
          <ul className="clean">
            {mos.map((m) => (
              <li key={m.id}>
                <span>
                  {m.ref} — {t('qty')} {m.qty} <Badge tone={statusTone(m.status)}>{moName(m.status)}</Badge>
                </span>
                {(m.status === 0 || m.status === 1) && (
                  <button onClick={() => advance(m)}>{m.status === 0 ? t('validate') : t('open')}</button>
                )}
                {m.status === 2 && token && (
                  <button className="primary" onClick={() => act(api.produceMO(token, m.id))}>
                    {t('produce')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {mos.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newMO')}</h4>
          <label className="field">
            {t('ref')} <input value={mref} onChange={(e) => setMref(e.target.value)} />
          </label>
          <label className="field">
            BOM ID <input value={mbom} onChange={(e) => setMbom(e.target.value)} />
          </label>
          <label className="field">
            {t('productId')} <input value={mproduct} onChange={(e) => setMproduct(e.target.value)} />
          </label>
          <label className="field">
            {t('warehouse')} ID <input value={mwh} onChange={(e) => setMwh(e.target.value)} />
          </label>
          <label className="field">
            {t('qty')} <input value={mqty} onChange={(e) => setMqty(e.target.value)} />
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
            {t('create')}
          </button>
        </Card>
      </div>
    </div>
  );
}
