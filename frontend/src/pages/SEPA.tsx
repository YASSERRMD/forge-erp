import { useEffect, useState } from 'react';
import { Landmark } from 'lucide-react';
import { apiExt, type RTransaction, type SepaBatch, type SepaMandate, type SepaTransfer } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

export function SEPA() {
  const { token } = useAuth();
  const { t } = useLang();
  const [mandates, setMandates] = useState<SepaMandate[]>([]);
  const [batches, setBatches] = useState<SepaBatch[]>([]);
  const [transfers, setTransfers] = useState<SepaTransfer[]>([]);
  const [rBatch, setRBatch] = useState('');
  const [rList, setRList] = useState<RTransaction[]>([]);
  const [umr, setUmr] = useState('');
  const [debtor, setDebtor] = useState('');
  const [iban, setIban] = useState('');
  const [bic, setBic] = useState('');
  const [batchRef, setBatchRef] = useState('');
  const [creditor, setCreditor] = useState('');
  const [creditorIban, setCreditorIban] = useState('');
  const [transferRef, setTransferRef] = useState('');
  const [transferDebtor, setTransferDebtor] = useState('');
  const [transferIban, setTransferIban] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.sepaMandates(token).then(setMandates).catch(() => undefined);
    apiExt.sepaBatches(token).then(setBatches).catch(() => undefined);
    apiExt.sepaTransfers(token).then(setTransfers).catch(() => undefined);
  };
  useEffect(reload, [token]);

  useEffect(() => {
    if (token && rBatch) {
      apiExt.sepaRTransactions(token, Number(rBatch)).then(setRList).catch(() => setRList([]));
    } else {
      setRList([]);
    }
  }, [token, rBatch]);

  const act = (fn: Promise<unknown>, msg: string) =>
    fn.then(() => {
      setError('');
      setNotice(msg);
      reload();
    }).catch((e: Error) => {
      setError(e.message);
      setNotice('');
    });

  const mandateStatus = (s: number) => (s === 1 ? 'signed' : s === -1 ? 'canceled' : 'draft');

  return (
    <div>
      <PageHeader icon={<Landmark size={22} />} title={t('sepa')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('mandates')}>
        <ul className="clean">
          {mandates.map((m) => (
            <li key={m.id}>
              {m.umr} — {m.debtor_name} <Badge tone={statusTone(m.status)}>{mandateStatus(m.status)}</Badge>{' '}
              {m.status === 0 && (
                <button onClick={() => token && act(apiExt.signSepaMandate(token, m.id, m.row_version), t('created'))}>
                  {t('sign')}
                </button>
              )}{' '}
              {(m.status === 0 || m.status === 1) && (
                <button onClick={() => token && act(apiExt.cancelSepaMandate(token, m.id, m.row_version), t('created'))}>
                  {t('cancel')}
                </button>
              )}
            </li>
          ))}
        </ul>
        {mandates.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('umr')} <input value={umr} onChange={(e) => setUmr(e.target.value)} />
        </label>
        <label className="field">
          {t('debtorName')} <input value={debtor} onChange={(e) => setDebtor(e.target.value)} />
        </label>
        <label className="field">
          {t('iban')} <input value={iban} onChange={(e) => setIban(e.target.value)} />
        </label>
        <label className="field">
          {t('bic')} <input value={bic} onChange={(e) => setBic(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.createSepaMandate(token, { umr, debtor_name: debtor, iban, bic, sequence: 'RCUR' }),
              t('created'),
            )
          }
        >
          {t('create')}
        </button>
      </Card>
      <Card title={t('batches')}>
        <ul className="clean">
          {batches.map((b) => (
            <li key={b.id}>
              {b.ref} — {b.creditor_name} <Badge tone={statusTone(b.status)}>{b.status}</Badge>{' '}
              {b.status === 0 && (
                <button onClick={() => token && act(apiExt.setSepaBatchStatus(token, b.id, 1, b.row_version), t('created'))}>
                  {t('validate')}
                </button>
              )}{' '}
              {b.status >= 1 && (
                <a href={apiExt.sepaBatchXmlUrl(b.id)} download>
                  {t('downloadXml')}
                </a>
              )}
            </li>
          ))}
        </ul>
        {batches.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('ref')} <input value={batchRef} onChange={(e) => setBatchRef(e.target.value)} />
        </label>
        <label className="field">
          {t('creditorName')} <input value={creditor} onChange={(e) => setCreditor(e.target.value)} />
        </label>
        <label className="field">
          {t('creditorIban')} <input value={creditorIban} onChange={(e) => setCreditorIban(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.createSepaBatch(token, {
                ref: batchRef,
                creditor_name: creditor,
                creditor_iban: creditorIban,
                creditor_bic: '',
                creditor_id: '',
                sequence: 'RCUR',
                transactions: [],
              }),
              t('created'),
            )
          }
        >
          {t('create')}
        </button>
      </Card>
      <Card title={t('rTransactions')}>
        <label className="field">
          {t('batches')}{' '}
          <select value={rBatch} onChange={(e) => setRBatch(e.target.value)}>
            <option value="">—</option>
            {batches.map((b) => (
              <option key={b.id} value={b.id}>
                {b.ref}
              </option>
            ))}
          </select>
        </label>
        <ul className="clean">
          {rList.map((r) => (
            <li key={r.id}>
              {r.end_to_end_id} <Badge>{r.kind}</Badge> {r.reason}
            </li>
          ))}
        </ul>
        {rBatch && rList.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('creditTransfers')}>
        <ul className="clean">
          {transfers.map((x) => (
            <li key={x.id}>
              {x.ref} — {x.debtor_name} <Badge tone={statusTone(x.status)}>{x.status}</Badge>{' '}
              {x.status === 0 && (
                <button onClick={() => token && act(apiExt.setSepaTransferStatus(token, x.id, 1, x.row_version), t('created'))}>
                  {t('validate')}
                </button>
              )}{' '}
              {x.status >= 1 && (
                <a href={apiExt.sepaTransferXmlUrl(x.id)} download>
                  {t('downloadXml')}
                </a>
              )}
            </li>
          ))}
        </ul>
        {transfers.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('ref')} <input value={transferRef} onChange={(e) => setTransferRef(e.target.value)} />
        </label>
        <label className="field">
          {t('debtorName')} <input value={transferDebtor} onChange={(e) => setTransferDebtor(e.target.value)} />
        </label>
        <label className="field">
          {t('debtorIban')} <input value={transferIban} onChange={(e) => setTransferIban(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.createSepaTransfer(token, {
                ref: transferRef,
                debtor_name: transferDebtor,
                debtor_iban: transferIban,
                debtor_bic: '',
                lines: [],
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
