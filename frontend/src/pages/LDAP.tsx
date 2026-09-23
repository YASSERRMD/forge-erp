import { useEffect, useState } from 'react';
import { Network } from 'lucide-react';
import { apiExt, type LdapUser } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

export function LDAP() {
  const { token } = useAuth();
  const { t } = useLang();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [users, setUsers] = useState<LdapUser[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.ldapStatus(token).then((s) => setEnabled(s.enabled)).catch(() => undefined);
    apiExt.ldapUsers(token).then(setUsers).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const sync = () => {
    if (!token) return;
    apiExt
      .ldapSync(token)
      .then((r) => {
        setError('');
        setNotice(`${t('synced')}: ${r.synced}`);
        reload();
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };

  return (
    <div>
      <PageHeader icon={<Network size={22} />} title={t('ldap')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('directoryStatus')}>
        {enabled !== null && (
          <p>
            <Badge tone={enabled ? 'ok' : 'warn'}>{enabled ? t('enabled') : t('disabled')}</Badge>
          </p>
        )}
        <button className="primary" onClick={sync} disabled={!enabled}>
          {t('syncNow')}
        </button>
        {!enabled && <p className="muted">{t('ldapDisabledHint')}</p>}
      </Card>
      <Card title={t('mirroredUsers')}>
        <ul className="clean">
          {users.map((u) => (
            <li key={u.login}>
              {u.login} <span className="muted">{u.full_name} — {u.email}</span>
            </li>
          ))}
        </ul>
        {users.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
