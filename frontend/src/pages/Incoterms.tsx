import { useEffect, useState } from 'react';
import { Ship } from 'lucide-react';
import { apiExt, type Incoterm } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Card, PageHeader } from '../components/ui';

export function Incoterms() {
  const { token } = useAuth();
  const { t } = useLang();
  const [terms, setTerms] = useState<Incoterm[]>([]);

  useEffect(() => {
    if (token) apiExt.incoterms(token).then(setTerms).catch(() => undefined);
  }, [token]);

  return (
    <div>
      <PageHeader icon={<Ship size={22} />} title={t('incoterms')} />
      <Card title={t('termList')}>
        <ul className="clean">
          {terms.map((x) => (
            <li key={x.code}>
              <strong>{x.code}</strong> — {x.label}
            </li>
          ))}
        </ul>
        {terms.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
