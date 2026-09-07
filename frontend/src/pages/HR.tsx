import { useEffect, useState } from 'react';
import { api, type ExpenseReport, type LeaveRequest } from '../api/client';
import { useAuth } from '../auth/AuthContext';

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
  const { token } = useAuth();
  const [leaves, setLeaves] = useState<LeaveRequest[]>([]);
  const [expenses, setExpenses] = useState<ExpenseReport[]>([]);
  useEffect(() => {
    if (token) {
      api.leaves(token).then(setLeaves).catch(() => undefined);
      api.expenses(token).then(setExpenses).catch(() => undefined);
    }
  }, [token]);
  return (
    <section>
      <h2>HR</h2>
      <h3>Leave requests</h3>
      <ul>
        {leaves.map((l) => (
          <li key={l.id}>
            {l.user_login} — {l.type} — {l.days}d ({leaveStatus[l.status] ?? l.status})
          </li>
        ))}
      </ul>
      <h3>Expense reports</h3>
      <ul>
        {expenses.map((e) => (
          <li key={e.id}>
            {e.ref} — {e.user_login} — {(e.total / 100).toFixed(2)} (
            {expenseStatus[e.status] ?? e.status})
          </li>
        ))}
      </ul>
    </section>
  );
}
