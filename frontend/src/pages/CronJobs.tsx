import { useEffect, useState } from 'react';
import { Timer } from 'lucide-react';
import { apiExt, type CronJob, type CronRun } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

export function CronJobs() {
  const { token } = useAuth();
  const { t } = useLang();
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [code, setCode] = useState('');
  const [interval, setInterval] = useState('3600');
  const [expr, setExpr] = useState('');
  const [runCode, setRunCode] = useState('');
  const [runs, setRuns] = useState<CronRun[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (token) apiExt.cronJobs(token).then(setJobs).catch(() => undefined);
  };
  useEffect(reload, [token]);

  useEffect(() => {
    if (token && runCode) {
      apiExt.cronRuns(token, runCode).then(setRuns).catch(() => setRuns([]));
    } else {
      setRuns([]);
    }
  }, [token, runCode]);

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
      <PageHeader icon={<Timer size={22} />} title={t('cronJobs')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('dueJobs')}>
        <ul className="clean">
          {jobs.map((j) => (
            <li key={j.id}>
              {j.code}{' '}
              <Badge tone={j.enabled ? 'ok' : undefined}>{j.enabled ? t('enabled') : t('disabled')}</Badge>{' '}
              <span className="muted">
                {t('lastStatus')}: {j.last_status || '—'}
              </span>{' '}
              <button onClick={() => token && act(apiExt.runCronJob(token, j.code), t('started'))}>
                {t('runNow')}
              </button>
            </li>
          ))}
        </ul>
        {jobs.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('upsertJob')}>
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
        <label className="field">
          {t('intervalS')} <input value={interval} onChange={(e) => setInterval(e.target.value)} />
        </label>
        <label className="field">
          {t('cronExpr')} <input value={expr} onChange={(e) => setExpr(e.target.value)} placeholder="0 * * * *" />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.upsertCronJob(token, {
                code,
                interval_s: Number(interval),
                cron_expr: expr,
                enabled: true,
              }),
              t('created'),
            )
          }
        >
          {t('save')}
        </button>
      </Card>
      <Card title={t('runHistory')}>
        <label className="field">
          {t('dueJobs')}{' '}
          <select value={runCode} onChange={(e) => setRunCode(e.target.value)}>
            <option value="">—</option>
            {jobs.map((j) => (
              <option key={j.id} value={j.code}>
                {j.code}
              </option>
            ))}
          </select>
        </label>
        <ul className="clean">
          {runs.map((r) => (
            <li key={r.id}>
              #{r.id} <Badge tone={r.status === 'ok' ? 'ok' : r.status === 'failed' ? 'bad' : 'warn'}>{r.status}</Badge>{' '}
              <span className="muted">{r.started_at}</span> {r.detail}
            </li>
          ))}
        </ul>
        {runCode && runs.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
