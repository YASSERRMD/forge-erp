import { useEffect, useState } from 'react';
import { Euro, Inbox } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

interface SepaBatch {
  id: number;
  ref: string;
  status: number;
  row_version: number;
}

interface Mailbox {
  code: string;
  host: string;
  active: boolean;
  last_error: string;
}

export function Collections() {
  const { token } = useAuth();
  const { t } = useLang();
  const [batches, setBatches] = useState<SepaBatch[]>([]);
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [mcode, setMcode] = useState('');
  const [mhost, setMhost] = useState('');
  const [mfrom, setMfrom] = useState('');
  const [msubject, setMsubject] = useState('');
  const [mbody, setMbody] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.sepaBatches(token).then((b) => setBatches(b as SepaBatch[])).catch(() => undefined);
    apiExt.mailboxes(token).then((m) => setMailboxes(m as Mailbox[])).catch(() => undefined);
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

  return (
    <div>
      <PageHeader icon={<Euro size={22} />} title={t('collections')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="alert-ok">{notice}</p>}
      <div className="grid two">
        <Card title="SEPA">
          <ul className="clean">
            {batches.map((b) => (
              <li key={b.id}>
                <span>{b.ref}</span>
                <Badge tone={statusTone(b.status)}>
                  {b.status === 1 ? t('stValidated') : b.status === 0 ? t('stDraft') : b.status === 2 ? t('stSent') : t('stCanceled')}
                </Badge>
                {b.status === 0 && token && (
                  <button onClick={() => act(apiExt.setSepaStatus(token, b.id, { status: 1, row_version: b.row_version }), t('stValidated'))}>
                    {t('validate')}
                  </button>
                )}
                {b.status === 1 && (
                  <a href={apiExt.sepaXMLUrl(b.id)} target="_blank" rel="noreferrer">
                    <button>XML</button>
                  </a>
                )}
              </li>
            ))}
          </ul>
          {batches.length === 0 && <p className="muted">{t('noData')}</p>}
        </Card>
        <Card title={t('mailbox')}>
          <ul className="clean">
            {mailboxes.map((m) => (
              <li key={m.code}>
                <span>
                  {m.code} — {m.host} {m.active ? '' : `(${t('stInactive')})`}
                </span>
                {m.last_error && <Badge tone="bad">{m.last_error}</Badge>}
              </li>
            ))}
          </ul>
          <h4>{t('newMailbox')}</h4>
          <label className="field">
            {t('code')} <input value={mcode} onChange={(e) => setMcode(e.target.value)} />
          </label>
          <label className="field">
            {t('host')} <input value={mhost} onChange={(e) => setMhost(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(apiExt.upsertMailbox(token, { code: mcode, host: mhost, port: 993, active: true }), t('save'))
            }
          >
            {t('save')}
          </button>
          <h4>{t('fileAsTicket')}</h4>
          <label className="field">
            {t('user')} <input value={mfrom} onChange={(e) => setMfrom(e.target.value)} />
          </label>
          <label className="field">
            {t('subject')} <input value={msubject} onChange={(e) => setMsubject(e.target.value)} />
          </label>
          <label className="field">
            {t('body')} <input value={mbody} onChange={(e) => setMbody(e.target.value)} />
          </label>
          <button
            onClick={() =>
              token &&
              act(
                apiExt.receiveMessage(token, {
                  mailbox: mcode,
                  from: mfrom,
                  subject: msubject,
                  body: mbody,
                }),
                t('create'),
              )
            }
          >
            <Inbox size={14} style={{ verticalAlign: '-2px' }} /> {t('fileAsTicket')}
          </button>
        </Card>
      </div>
    </div>
  );
}
