import { useEffect, useState } from 'react';
import { Coins } from 'lucide-react';
import { apiExt, type FxRate } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function FXRates() {
  const { token } = useAuth();
  const { t } = useLang();
  const [rates, setRates] = useState<FxRate[]>([]);
  const [code, setCode] = useState('');
  const [rate, setRate] = useState('');
  const [amount, setAmount] = useState('100');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [result, setResult] = useState<number | null>(null);
  const [error, setError] = useState('');

  const reload = () => {
    if (token) apiExt.fxRates(token).then(setRates).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const save = () => {
    if (!token) return;
    apiExt
      .setFxRate(token, { code: code.toUpperCase(), rate_to_base: Math.round(Number(rate) * 1000000) })
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  const convert = () => {
    if (!token) return;
    apiExt
      .fxConvert(token, Math.round(Number(amount) * 100), from, to)
      .then((r) => {
        setResult(r.result);
        setError('');
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<Coins size={22} />} title={t('fxRates')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('boardRates')}>
        <ul className="clean">
          {rates.map((r) => (
            <li key={r.code}>
              {r.code}: <strong>{(r.rate_to_base / 1000000).toFixed(6)}</strong>
            </li>
          ))}
        </ul>
        {rates.length === 0 && <p className="muted">{t('noData')}</p>}
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="EUR" />
        </label>
        <label className="field">
          {t('rateToBase')} <input value={rate} onChange={(e) => setRate(e.target.value)} placeholder="1.0800" />
        </label>
        <button className="primary" onClick={save}>
          {t('save')}
        </button>
      </Card>
      <Card title={t('convertTester')}>
        <label className="field">
          {t('amount')} <input value={amount} onChange={(e) => setAmount(e.target.value)} />
        </label>
        <label className="field">
          {t('value')}{' '}
          <select value={from} onChange={(e) => setFrom(e.target.value)}>
            <option value="">—</option>
            {rates.map((r) => (
              <option key={r.code} value={r.code}>
                {r.code}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('value')}{' '}
          <select value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">—</option>
            {rates.map((r) => (
              <option key={r.code} value={r.code}>
                {r.code}
              </option>
            ))}
          </select>
        </label>
        <button className="primary" onClick={convert}>
          {t('convert')}
        </button>
        {result !== null && (
          <p>
            {t('result')}: <strong>{(result / 100).toFixed(2)}</strong>
          </p>
        )}
      </Card>
    </div>
  );
}
