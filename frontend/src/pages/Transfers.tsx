import { useEffect, useState } from 'react';
import { ArrowLeftRight } from 'lucide-react';
import { api, apiExt, type Product, type StockTransfer, type TransferLine, type Warehouse } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

const STATUS = { 0: 'draft', 1: 'validated', 9: 'canceled' } as const;

export function Transfers() {
  const { token } = useAuth();
  const { t } = useLang();
  const [list, setList] = useState<StockTransfer[]>([]);
  const [warehouses, setWarehouses] = useState<Warehouse[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [src, setSrc] = useState('');
  const [dst, setDst] = useState('');
  const [note, setNote] = useState('');
  const [openId, setOpenId] = useState<number | null>(null);
  const [lines, setLines] = useState<TransferLine[]>([]);
  const [lineProduct, setLineProduct] = useState('');
  const [lineQty, setLineQty] = useState('1');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.transfers(token).then(setList).catch(() => undefined);
    apiExt.warehouses(token).then(setWarehouses).catch(() => undefined);
    api.products(token).then(setProducts).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>, msg: string) =>
    fn.then(() => {
      setError('');
      setNotice(msg);
      reload();
    }).catch((e: Error) => {
      setError(e.message);
      setNotice('');
    });

  const open = (id: number) => {
    if (!token) return;
    setOpenId(id);
    apiExt.transferLines(token, id).then(setLines).catch(() => setLines([]));
  };

  const whName = (id: number) => warehouses.find((w) => w.id === id)?.code ?? `#${id}`;
  const prodName = (id: number) => products.find((p) => p.id === id)?.name ?? `#${id}`;

  return (
    <div>
      <PageHeader icon={<ArrowLeftRight size={22} />} title={t('transfers')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('transferList')}>
        <ul className="clean">
          {list.map((tr) => (
            <li key={tr.id}>
              <button className="linklike" onClick={() => open(tr.id)}>
                {tr.ref}
              </button>{' '}
              <span className="muted">
                {whName(tr.source_warehouse_id)} → {whName(tr.dest_warehouse_id)}
              </span>{' '}
              <Badge tone={statusTone(tr.status)}>{t(STATUS[tr.status as keyof typeof STATUS] ?? 'draft')}</Badge>{' '}
              {tr.status === 0 && (
                <>
                  <button onClick={() => token && act(apiExt.validateTransfer(token, tr.id), t('validated'))}>
                    {t('validate')}
                  </button>{' '}
                  <button onClick={() => token && act(apiExt.cancelTransfer(token, tr.id), t('canceled'))}>
                    {t('cancel')}
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
        {list.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      {openId !== null && (
        <Card title={`${t('transferLines')} (#${openId})`}>
          <ul className="clean">
            {lines.map((l) => (
              <li key={l.id}>
                {prodName(l.product_id)} × {l.qty}{' '}
                <span className="muted">{money(l.unit_cost)}</span>
              </li>
            ))}
          </ul>
          {lines.length === 0 && <p className="muted">{t('noData')}</p>}
          <label className="field">
            {t('product')}{' '}
            <select value={lineProduct} onChange={(e) => setLineProduct(e.target.value)}>
              <option value="">—</option>
              {products.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            {t('quantity')}{' '}
            <input value={lineQty} onChange={(e) => setLineQty(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              openId !== null &&
              act(
                apiExt.addTransferLine(token, openId, {
                  product_id: Number(lineProduct),
                  qty: Number(lineQty),
                }),
                t('created'),
              ).then(() => open(openId))
            }
          >
            {t('addLine')}
          </button>
        </Card>
      )}
      <Card title={t('newTransfer')}>
        <label className="field">
          {t('sourceWarehouse')}{' '}
          <select value={src} onChange={(e) => setSrc(e.target.value)}>
            <option value="">—</option>
            {warehouses.map((w) => (
              <option key={w.id} value={w.id}>
                {w.code} — {w.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('destWarehouse')}{' '}
          <select value={dst} onChange={(e) => setDst(e.target.value)}>
            <option value="">—</option>
            {warehouses.map((w) => (
              <option key={w.id} value={w.id}>
                {w.code} — {w.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('note')} <input value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.createTransfer(token, {
                source_warehouse_id: Number(src),
                dest_warehouse_id: Number(dst),
                note,
              }),
              t('created'),
            )
          }
        >
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
