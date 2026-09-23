import { useState } from 'react';
import { KeyRound } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function PortalAdmin() {
  const { token } = useAuth();
  const { t } = useLang();
  const [orgId, setOrgId] = useState('');
  const [ttl, setTtl] = useState('90');
  const [minted, setMinted] = useState('');
  const [revokeId, setRevokeId] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const mint = () => {
    if (!token) return;
    apiExt
      .mintPortalToken(token, { org_id: Number(orgId), ttl_days: Number(ttl) })
      .then((r) => {
        setMinted(r.token);
        setError('');
        setNotice(`${t('created')} #${r.id}`);
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };

  const revoke = () => {
    if (!token || !revokeId) return;
    apiExt
      .revokePortalToken(token, Number(revokeId))
      .then(() => {
        setError('');
        setNotice(t('revoked'));
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };

  return (
    <div>
      <PageHeader icon={<KeyRound size={22} />} title={t('portalAdmin')} sub={t('portalAdminSub')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('mintToken')}>
        <label className="field">
          {t('organizations')} <input value={orgId} onChange={(e) => setOrgId(e.target.value)} />
        </label>
        <label className="field">
          {t('ttlDays')} <input value={ttl} onChange={(e) => setTtl(e.target.value)} />
        </label>
        <button className="primary" onClick={mint}>
          {t('create')}
        </button>
        {minted && (
          <p>
            {t('tokenOnce')}: <code>{minted}</code>
          </p>
        )}
      </Card>
      <Card title={t('revokeToken')}>
        <label className="field">
          {t('value')} <input value={revokeId} onChange={(e) => setRevokeId(e.target.value)} />
        </label>
        <button className="primary" onClick={revoke}>
          {t('revoke')}
        </button>
      </Card>
    </div>
  );
}
