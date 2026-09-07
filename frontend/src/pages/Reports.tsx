import { useEffect, useState } from 'react';
import { api } from '../api/client';
import { useAuth } from '../auth/AuthContext';

export function Reports() {
  const { token } = useAuth();
  const [pnl, setPnl] = useState({ revenue: 0, expense: 0, net: 0 });
  const [receivable, setReceivable] = useState(0);
  useEffect(() => {
    if (token) {
      api.pnl(token).then(setPnl).catch(() => undefined);
      api
        .receivables(token)
        .then((r) => setReceivable(r.total))
        .catch(() => undefined);
    }
  }, [token]);
  const money = (v: number) => (v / 100).toFixed(2);
  return (
    <section>
      <h2>Reports</h2>
      <h3>Profit &amp; loss (posted entries)</h3>
      <p>
        Revenue {money(pnl.revenue)} — Expense {money(pnl.expense)} — Net{' '}
        {money(pnl.net)}
      </p>
      <h3>Receivables</h3>
      <p>Outstanding {money(receivable)}</p>
    </section>
  );
}
