import { useEffect, useState } from 'react';
import { Inbox } from 'lucide-react';
import { apiExt, type Mailbox } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

export function Inbound() {
  const { token } = useAuth();
  const { t } = useLang();
  const [boxes, setBoxes] = useState<Mailbox[]>([]);
  const [code, setCode] = useState('');
  const [host, setHost] = useState('');
  const [port, setPort] = useState('993');
  const [username, setUsername] = useState('');
  const [mailbox, setMailbox] = useState('');
  const [from, setFrom] = useState('');
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (token) apiExt.inboundMailboxes(token).then(setBoxes).catch(() => undefined);
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
      <PageHeader icon={<Inbox size={22} />} title={t('inbound')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('mailboxes')}>
        <ul className="clean">
          {boxes.map((b) => (
            <li key={b.id}>
              {b.code} — {b.host}:{b.port} ({b.username}){' '}
              <Badge tone={b.active ? 'ok' : 'warn'}>{b.active ? t('enabled') : t('disabled')}</Badge>{' '}
              {b.last_error && <span className="muted">{b.last_error}</span>}
            </li>
          ))}
        </ul>
        {boxes.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
        <label className="field">
          {t('host')} <input value={host} onChange={(e) => setHost(e.target.value)} />
        </label>
        <label className="field">
          {t('port')} <input value={port} onChange={(e) => setPort(e.target.value)} />
        </label>
        <label className="field">
          {t('username')} <input value={username} onChange={(e) => setUsername(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.upsertMailbox(token, {
                code,
                host,
                port: Number(port),
                username,
                use_tls: true,
                active: true,
              }),
              t('created'),
            )
          }
        >
          {t('save')}
        </button>
      </Card>
      <Card title={t('intakeTest')}>
        <label className="field">
          {t('mailboxes')}{' '}
          <select value={mailbox} onChange={(e) => setMailbox(e.target.value)}>
            <option value="">—</option>
            {boxes.map((b) => (
              <option key={b.id} value={b.code}>
                {b.code}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('from')} <input value={from} onChange={(e) => setFrom(e.target.value)} />
        </label>
        <label className="field">
          {t('label')} <input value={subject} onChange={(e) => setSubject(e.target.value)} />
        </label>
        <label className="field">
          {t('value')}{' '}
          <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={2} style={{ width: '100%' }} />
        </label>
        <button
          className="primary"
          onClick={() => token && act(apiExt.inboundReceive(token, { mailbox, from, subject, body }), t('created'))}
        >
          {t('fileTicket')}
        </button>
      </Card>
    </div>
  );
}
