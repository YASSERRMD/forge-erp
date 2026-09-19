import { useEffect, useState } from 'react';
import { Mail } from 'lucide-react';
import { apiExt, type MailingCampaign } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

export function Mailing() {
  const { token } = useAuth();
  const { t } = useLang();
  const [list, setList] = useState<MailingCampaign[]>([]);
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [queueId, setQueueId] = useState('');
  const [emails, setEmails] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (token) apiExt.mailingCampaigns(token).then(setList).catch(() => undefined);
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

  const queue = () => {
    if (!token || !queueId) return;
    const extra = emails.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean);
    act(
      apiExt.queueMailing(token, Number(queueId), extra).then((r) => `${r.queued}`),
      t('queued'),
    );
  };

  return (
    <div>
      <PageHeader icon={<Mail size={22} />} title={t('mailing')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('campaignList')}>
        <ul className="clean">
          {list.map((c) => (
            <li key={c.id}>
              #{c.id} {c.subject} <Badge>{c.status}</Badge>{' '}
              <button onClick={() => token && act(apiExt.sendMailing(token, c.id), t('sent'))}>
                {t('send')}
              </button>
            </li>
          ))}
        </ul>
        {list.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('newCampaign')}>
        <label className="field">
          {t('label')} <input value={subject} onChange={(e) => setSubject(e.target.value)} />
        </label>
        <label className="field">
          {t('value')} <input value={body} onChange={(e) => setBody(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() => token && act(apiExt.createMailingCampaign(token, { subject, body }), t('created'))}
        >
          {t('create')}
        </button>
      </Card>
      <Card title={t('queueRecipients')}>
        <label className="field">
          {t('campaignList')}{' '}
          <select value={queueId} onChange={(e) => setQueueId(e.target.value)}>
            <option value="">—</option>
            {list.map((c) => (
              <option key={c.id} value={c.id}>
                #{c.id} {c.subject}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('extraEmails')} <input value={emails} onChange={(e) => setEmails(e.target.value)} />
        </label>
        <button className="primary" onClick={queue}>
          {t('queue')}
        </button>
      </Card>
    </div>
  );
}
