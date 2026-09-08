import { useEffect, useState } from 'react';
import { Users } from 'lucide-react';
import { apiExt, type Donation, type Member } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

export function Members() {
  const { token } = useAuth();
  const { t } = useLang();
  const [members, setMembers] = useState<Member[]>([]);
  const [donations, setDonations] = useState<Donation[]>([]);
  const [error, setError] = useState('');
  const [ref, setRef] = useState('');
  const [first, setFirst] = useState('');
  const [last, setLast] = useState('');
  const [dref, setDref] = useState('');
  const [ddonor, setDdonor] = useState('');
  const [damount, setDamount] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.members(token).then(setMembers).catch(() => undefined);
    apiExt.donations(token).then(setDonations).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  const memberName = (s: number) =>
    s === 1 ? t('stActive') : s === 0 ? t('stDraft') : s === -1 ? t('stResigned') : t('stExcluded');
  const donationName = (s: number) =>
    s === 1 ? t('stPaid') : s === 0 ? t('stPromised') : t('stCanceled');

  return (
    <div>
      <PageHeader icon={<Users size={22} />} title={t('members')} />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title={t('members')}>
          <ul className="clean">
            {members.map((m) => (
              <li key={m.id}>
                <span>
                  {m.ref} — {m.first_name} {m.last_name} {m.company}
                </span>
                <Badge tone={statusTone(m.status)}>{memberName(m.status)}</Badge>
                {m.status === 0 && token && (
                  <button onClick={() => act(apiExt.setMemberStatus(token, m.id, { status: 1, row_version: m.row_version }))}>
                    {t('validate')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {members.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newMember')}</h4>
          <label className="field">
            {t('ref')} <input value={ref} onChange={(e) => setRef(e.target.value)} />
          </label>
          <label className="field">
            {t('firstName')} <input value={first} onChange={(e) => setFirst(e.target.value)} />
          </label>
          <label className="field">
            {t('lastName')} <input value={last} onChange={(e) => setLast(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(
                apiExt.createMember(token, {
                  ref,
                  type_id: 1,
                  first_name: first,
                  last_name: last,
                }),
              )
            }
          >
            {t('create')}
          </button>
        </Card>
        <Card title={t('donations')}>
          <ul className="clean">
            {donations.map((d) => (
              <li key={d.id}>
                <span>
                  {d.ref} — {d.donor_name} — {money(d.amount)}
                </span>
                <Badge tone={statusTone(d.status)}>{donationName(d.status)}</Badge>
                {d.status === 0 && token && (
                  <button
                    onClick={() =>
                      act(apiExt.setDonationStatus(token, d.id, { status: 1, row_version: d.row_version }))
                    }
                  >
                    {t('stPaid')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {donations.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newDonation')}</h4>
          <label className="field">
            {t('ref')} <input value={dref} onChange={(e) => setDref(e.target.value)} />
          </label>
          <label className="field">
            {t('user')} <input value={ddonor} onChange={(e) => setDdonor(e.target.value)} />
          </label>
          <label className="field">
            {t('amount')} <input value={damount} onChange={(e) => setDamount(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(
                apiExt.createDonation(token, {
                  ref: dref,
                  donor_name: ddonor,
                  amount: Math.round(Number(damount) * 100),
                  donated_at: new Date().toISOString(),
                  method: 'transfer',
                }),
              )
            }
          >
            {t('create')}
          </button>
        </Card>
      </div>
    </div>
  );
}
