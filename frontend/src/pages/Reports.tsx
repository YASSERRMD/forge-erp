import { useEffect, useState } from 'react';
import { BarChart3 } from 'lucide-react';
import { api } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Card, PageHeader } from '../components/ui';
import { money } from '../components/ui';

export function Reports() {
  const { token } = useAuth();
  const { t } = useLang();
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
    </div>
  );
}
