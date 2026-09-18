import { useEffect, useState } from 'react';
import { Handshake } from 'lucide-react';
import { apiExt, type PartnerProgram, type Referral } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Partnerships() {
  const { token } = useAuth();
  const { t } = useLang();
  const [programs, setPrograms] = useState<PartnerProgram[]>([]);
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [programId, setProgramId] = useState('');
  const [referrals, setReferrals] = useState<Referral[]>([]);
  const [referrerOrg, setReferrerOrg] = useState('');
  const [referredOrg, setReferredOrg] = useState('');
  const [referralCode, setReferralCode] = useState('');
  const [accrualReferral, setAccrualReferral] = useState('');
  const [saleTotal, setSaleTotal] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (token) apiExt.partnerPrograms(token).then(setPrograms).catch(() => undefined);
  };
  useEffect(reload, [token]);

  useEffect(() => {
    if (token && programId) {
      apiExt.programReferrals(token, Number(programId)).then(setReferrals).catch(() => setReferrals([]));
    } else {
      setReferrals([]);
    }
  }, [token, programId]);

  const act = (fn: Promise<unknown>, msg: string) =>
    fn.then(() => {
      setError('');
      setNotice(msg);
      reload();
    }).catch((e: Error) => {
      setError(e.message);
      setNotice('');
    });

  return (
    <div>
      <PageHeader icon={<Handshake size={22} />} title={t('partnerships')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('programList')}>
        <ul className="clean">
          {programs.map((p) => (
            <li key={p.id}>
              {p.code} — {p.name}{' '}
              <span className="muted">
                ({p.tiers.length} {t('lines')})
              </span>
            </li>
          ))}
        </ul>
        {programs.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
        <label className="field">
          {t('name')} <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() => token && act(apiExt.createPartnerProgram(token, { code, name }), t('created'))}
        >
          {t('create')}
        </button>
      </Card>
      <Card title={t('referrals')}>
        <label className="field">
          {t('programList')}{' '}
          <select value={programId} onChange={(e) => setProgramId(e.target.value)}>
            <option value="">—</option>
            {programs.map((p) => (
              <option key={p.id} value={p.id}>
                {p.code}
              </option>
            ))}
          </select>
        </label>
        {programId && (
          <>
            <ul className="clean">
              {referrals.map((r) => (
                <li key={r.id}>
                  {r.code} <span className="muted">({r.status})</span>
                </li>
              ))}
            </ul>
            <label className="field">
              {t('referrerOrg')} <input value={referrerOrg} onChange={(e) => setReferrerOrg(e.target.value)} />
            </label>
            <label className="field">
              {t('referredOrg')} <input value={referredOrg} onChange={(e) => setReferredOrg(e.target.value)} />
            </label>
            <label className="field">
              {t('code')} <input value={referralCode} onChange={(e) => setReferralCode(e.target.value)} />
            </label>
            <button
              className="primary"
              onClick={() =>
                token &&
                act(
                  apiExt.registerReferral(token, Number(programId), {
                    referrer_org_id: Number(referrerOrg),
                    referred_org_id: Number(referredOrg),
                    code: referralCode,
                  }),
                  t('created'),
                ).then(() =>
                  apiExt.programReferrals(token, Number(programId)).then(setReferrals).catch(() => undefined),
                )
              }
            >
              {t('create')}
            </button>
          </>
        )}
      </Card>
      <Card title={t('accrue')}>
        <label className="field">
          {t('referrals')}{' '}
          <select value={accrualReferral} onChange={(e) => setAccrualReferral(e.target.value)}>
            <option value="">—</option>
            {referrals.map((r) => (
              <option key={r.id} value={r.id}>
                {r.code}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('saleTotal')} <input value={saleTotal} onChange={(e) => setSaleTotal(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() =>
            token &&
            accrualReferral &&
            act(apiExt.accrueCommission(token, Number(accrualReferral), Math.round(Number(saleTotal) * 100)), t('created'))
          }
        >
          {t('accrue')}
        </button>
      </Card>
    </div>
  );
}
