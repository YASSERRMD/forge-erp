import { useEffect, useState } from 'react';
import { ShieldHalf } from 'lucide-react';
import { apiExt, type ErasureRequest, type RetentionRule } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

const SCOPES = ['orgs', 'members'];

export function DataPolicy() {
  const { token } = useAuth();
  const { t } = useLang();
  const [rules, setRules] = useState<RetentionRule[]>([]);
  const [scope, setScope] = useState('orgs');
  const [retainDays, setRetainDays] = useState('365');
  const [due, setDue] = useState<{ scope: string; due_count: number; candidates: Array<{ subject_id: number }> } | null>(null);
  const [erasures, setErasures] = useState<ErasureRequest[]>([]);
  const [erasureScope, setErasureScope] = useState('orgs');
  const [subjectId, setSubjectId] = useState('');
  const [reason, setReason] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.retentionRules(token).then(setRules).catch(() => undefined);
    apiExt.erasureRequests(token).then(setErasures).catch(() => undefined);
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

  const dryRun = () => {
    if (!token) return;
    apiExt
      .retentionDryRun(token, scope)
      .then((r) => {
        setDue(r);
        setError('');
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<ShieldHalf size={22} />} title={t('dataPolicy')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('retentionRules')}>
        <ul className="clean">
          {rules.map((r) => (
            <li key={r.scope}>
              {r.scope}: {r.retain_days} {t('days')} → {r.action}
            </li>
          ))}
        </ul>
        {rules.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('value')}{' '}
          <select value={scope} onChange={(e) => setScope(e.target.value)}>
            {SCOPES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('retainDays')} <input value={retainDays} onChange={(e) => setRetainDays(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token && act(apiExt.upsertRetentionRule(token, { scope, retain_days: Number(retainDays), action: 'anonymize' }), t('created'))
          }
        >
          {t('save')}
        </button>{' '}
        <button onClick={dryRun}>{t('dryRun')}</button>
        {due && (
          <p>
            {due.scope}: <strong>{due.due_count}</strong>
          </p>
        )}
      </Card>
      <Card title={t('erasures')}>
        <ul className="clean">
          {erasures.map((e) => (
            <li key={e.id}>
              #{e.id} {e.scope}/{e.subject_id} <Badge>{e.status}</Badge>{' '}
              {e.status !== 'done' && (
                <button onClick={() => token && act(apiExt.completeErasure(token, e.id), t('created'))}>
                  {t('complete')}
                </button>
              )}
            </li>
          ))}
        </ul>
        {erasures.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('value')}{' '}
          <select value={erasureScope} onChange={(e) => setErasureScope(e.target.value)}>
            {SCOPES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('subjectId')} <input value={subjectId} onChange={(e) => setSubjectId(e.target.value)} />
        </label>
        <label className="field">
          {t('reason')} <input value={reason} onChange={(e) => setReason(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            act(
              apiExt.requestErasure(token, { scope: erasureScope, subject_id: Number(subjectId), reason }),
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
