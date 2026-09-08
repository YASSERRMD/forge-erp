import { useEffect, useState } from 'react';
import { Users } from 'lucide-react';
import { api, apiExt, type ExpenseReport, type LeaveRequest } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

export function HR() {
  const { token, login } = useAuth();
  const { t } = useLang();
  const [leaves, setLeaves] = useState<LeaveRequest[]>([]);
  const [expenses, setExpenses] = useState<ExpenseReport[]>([]);
  const [error, setError] = useState('');
  const [luser, setLuser] = useState('');
  const [ltype, setLtype] = useState('paid');
  const [lstart, setLstart] = useState('');
  const [lend, setLend] = useState('');
  const [eref, setEref] = useState('');
  const [euser, setEuser] = useState('');
  const [suser, setSuser] = useState('');
  const [speriod, setSperiod] = useState('');
  const [sgross, setSgross] = useState('');
  const [scharges, setScharges] = useState('');

  const leaveName = (s: number) =>
    s === 0 ? t('stDraft') : s === 1 ? t('stPending') : s === 2 ? t('stApproved') : s === -1 ? t('stRejected') : t('stCanceled');
  const expenseName = (s: number) =>
    s === 0 ? t('stDraft') : s === 1 ? t('stPending') : s === 2 ? t('stValidated') : s === 3 ? t('stPaid') : s === -1 ? t('stRejected') : t('stCanceled');

  const reload = () => {
    if (!token) return;
    api.leaves(token).then(setLeaves).catch(() => undefined);
    api.expenses(token).then(setExpenses).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  return (
    <div>
      <PageHeader icon={<Users size={22} />} title={t('hr')} sub="Leave, expenses, payroll" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title={t('newLeave')}>
          <ul className="clean">
            {leaves.map((l) => (
              <li key={l.id}>
                <span>
                  {l.user_login} — {l.type} — {l.days}{t('days')} <Badge tone={statusTone(l.status)}>{leaveName(l.status)}</Badge>
                </span>
                {l.status === 0 && token && (
                  <button onClick={() => act(apiExt.setLeaveStatus(token, l.id, { status: 1, row_version: l.row_version }))}>
                    {t('submit')}
                  </button>
                )}
                {l.status === 1 && token && (
                  <button onClick={() => act(apiExt.setLeaveStatus(token, l.id, { status: 2, row_version: l.row_version }))}>
                    {t('approve')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>{t('newLeave')}</h4>
          <label className="field">
            {t('user')} <input value={luser} placeholder={login ?? ''} onChange={(e) => setLuser(e.target.value)} />
          </label>
          <label className="field">
            {t('type')}{' '}
            <select value={ltype} onChange={(e) => setLtype(e.target.value)}>
              <option value="paid">paid</option>
              <option value="sick">sick</option>
              <option value="unpaid">unpaid</option>
            </select>
          </label>
          <label className="field">
            {t('start')} <input type="date" value={lstart} onChange={(e) => setLstart(e.target.value)} />
          </label>
          <label className="field">
            {t('end')} <input type="date" value={lend} onChange={(e) => setLend(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(
                apiExt.createLeave(token, {
                  user_login: luser || (login ?? ''),
                  type: ltype,
                  start_at: new Date(lstart).toISOString(),
                  end_at: new Date(lend).toISOString(),
                }),
              )
            }
          >
            {t('create')}
          </button>
        </Card>
        <Card title={t('newReport')}>
          <ul className="clean">
            {expenses.map((e) => (
              <li key={e.id}>
                <span>
                  {e.ref} — {e.user_login} — {money(e.total)}{' '}
                  <Badge tone={statusTone(e.status)}>{expenseName(e.status)}</Badge>
                </span>
                {e.status === 0 && token && (
                  <button onClick={() => act(apiExt.setExpenseStatus(token, e.id, { status: 1, row_version: e.row_version }))}>
                    {t('submit')}
                  </button>
                )}
                {e.status === 1 && token && (
                  <button onClick={() => act(apiExt.setExpenseStatus(token, e.id, { status: 2, row_version: e.row_version }))}>
                    {t('approve')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>{t('newReport')}</h4>
          <label className="field">
            {t('ref')} <input value={eref} onChange={(e) => setEref(e.target.value)} />
          </label>
          <label className="field">
            {t('user')} <input value={euser} placeholder={login ?? ''} onChange={(e) => setEuser(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(apiExt.createExpense(token, { ref: eref, user_login: euser || (login ?? '') }))}
          >
            {t('create')}
          </button>
        </Card>
      </div>
      <Card title={t('newSalary')}>
        <label className="field">
          {t('user')} <input value={suser} placeholder={login ?? ''} onChange={(e) => setSuser(e.target.value)} />
        </label>
        <label className="field">
          {t('period')} <input placeholder="2026-09" value={speriod} onChange={(e) => setSperiod(e.target.value)} />
        </label>
        <label className="field">
          {t('gross')} <input value={sgross} onChange={(e) => setSgross(e.target.value)} />
        </label>
        <label className="field">
          {t('charges')} <input value={scharges} onChange={(e) => setScharges(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() => {
            if (!token) return;
            const gross = Math.round(Number(sgross) * 100);
            const charges = Math.round(Number(scharges) * 100);
            act(
              apiExt.createSalary(token, {
                user_login: suser || (login ?? ''),
                period: speriod,
                gross,
                charges,
                net: gross - charges,
              }),
            );
          }}
        >
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
