import { useEffect, useState } from 'react';
import { Users } from 'lucide-react';
import { api, apiExt, type ExpenseReport, type LeaveRequest } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

const leaveStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Submitted',
  2: 'Approved',
  [-1]: 'Rejected',
  [-2]: 'Canceled',
};

const expenseStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Submitted',
  2: 'Approved',
  3: 'Paid',
  [-1]: 'Rejected',
  [-2]: 'Canceled',
};

export function HR() {
  const { token, login } = useAuth();
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
      <PageHeader icon={<Users size={22} />} title="HR" sub="Leave, expenses, payroll" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Leave requests">
          <ul className="clean">
            {leaves.map((l) => (
              <li key={l.id}>
                <span>
                  {l.user_login} — {l.type} — {l.days}d{' '}
                  <Badge tone={statusTone(l.status)}>{leaveStatus[l.status] ?? l.status}</Badge>
                </span>
                {l.status === 0 && token && (
                  <button onClick={() => act(apiExt.setLeaveStatus(token, l.id, { status: 1, row_version: l.row_version }))}>
                    Submit
                  </button>
                )}
                {l.status === 1 && token && (
                  <button onClick={() => act(apiExt.setLeaveStatus(token, l.id, { status: 2, row_version: l.row_version }))}>
                    Approve
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>Request leave</h4>
          <label className="field">
            User <input value={luser} placeholder={login ?? ''} onChange={(e) => setLuser(e.target.value)} />
          </label>
          <label className="field">
            Type{' '}
            <select value={ltype} onChange={(e) => setLtype(e.target.value)}>
              <option value="paid">paid</option>
              <option value="sick">sick</option>
              <option value="unpaid">unpaid</option>
            </select>
          </label>
          <label className="field">
            Start <input type="date" value={lstart} onChange={(e) => setLstart(e.target.value)} />
          </label>
          <label className="field">
            End <input type="date" value={lend} onChange={(e) => setLend(e.target.value)} />
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
            File
          </button>
        </Card>
        <Card title="Expense reports">
          <ul className="clean">
            {expenses.map((e) => (
              <li key={e.id}>
                <span>
                  {e.ref} — {e.user_login} — {money(e.total)}{' '}
                  <Badge tone={statusTone(e.status)}>{expenseStatus[e.status] ?? e.status}</Badge>
                </span>
                {e.status === 0 && token && (
                  <button onClick={() => act(apiExt.setExpenseStatus(token, e.id, { status: 1, row_version: e.row_version }))}>
                    Submit
                  </button>
                )}
                {e.status === 1 && token && (
                  <button onClick={() => act(apiExt.setExpenseStatus(token, e.id, { status: 2, row_version: e.row_version }))}>
                    Approve
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New report</h4>
          <label className="field">
            Ref <input value={eref} onChange={(e) => setEref(e.target.value)} />
          </label>
          <label className="field">
            User <input value={euser} placeholder={login ?? ''} onChange={(e) => setEuser(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(apiExt.createExpense(token, { ref: eref, user_login: euser || (login ?? '') }))}
          >
            Create
          </button>
        </Card>
      </div>
      <Card title="Record salary">
        <label className="field">
          User <input value={suser} placeholder={login ?? ''} onChange={(e) => setSuser(e.target.value)} />
        </label>
        <label className="field">
          Period <input placeholder="2026-09" value={speriod} onChange={(e) => setSperiod(e.target.value)} />
        </label>
        <label className="field">
          Gross <input value={sgross} onChange={(e) => setSgross(e.target.value)} />
        </label>
        <label className="field">
          Charges <input value={scharges} onChange={(e) => setScharges(e.target.value)} />
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
          Record
        </button>
      </Card>
    </div>
  );
}
