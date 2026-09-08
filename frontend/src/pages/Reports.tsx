import { useEffect, useState } from 'react';
import { BarChart3 } from 'lucide-react';
import { api, apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Card, PageHeader } from '../components/ui';
import { money } from '../components/ui';

export function Reports() {
  const { token } = useAuth();
  const { t } = useLang();
  const [pnl, setPnl] = useState({ revenue: 0, expense: 0, net: 0 });
  const [receivable, setReceivable] = useState(0);
  const [margins, setMargins] = useState<
    Array<{ product_id: number; sku: string; qty: number; revenue: number; cost: number; margin: number; margin_pct: number }>
  >([]);
  useEffect(() => {
    if (token) {
      api.pnl(token).then(setPnl).catch(() => undefined);
      api
        .receivables(token)
        .then((r) => setReceivable(r.total))
        .catch(() => undefined);
      apiExt.margins(token).then(setMargins).catch(() => undefined);
    }
  }, [token]);
  return (
    <div>
      <PageHeader icon={<BarChart3 size={22} />} title={t('reports')} sub={t('subtitle')} />
      <div className="grid two">
        <Card title={t('pnlMix')}>
          <p>
            {t('revenue')} {money(pnl.revenue)} — {t('expenseWord')} {money(pnl.expense)} — {t('net')}{' '}
            {money(pnl.net)}
          </p>
        </Card>
        <Card title={t('receivables')}>
          <p>
            {t('balance')} {money(receivable)}
          </p>
        </Card>
      </div>
      <Card title={t('margins')}>
        <table className="data">
          <thead>
            <tr>
              <th>{t('sku')}</th>
              <th>{t('qty')}</th>
              <th>{t('revenue')}</th>
              <th>{t('cost')}</th>
              <th>Margin %</th>
            </tr>
          </thead>
          <tbody>
            {margins.map((m) => (
              <tr key={m.product_id}>
                <td>{m.sku}</td>
                <td>{m.qty}</td>
                <td>{money(m.revenue)}</td>
                <td>{money(m.cost)}</td>
                <td>{(m.margin_pct / 100).toFixed(1)}%</td>
              </tr>
            ))}
          </tbody>
        </table>
        {margins.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
