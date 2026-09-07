import { useEffect, useState } from 'react';
import { CreditCard } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

interface Attempt {
  id: number;
  ref: string;
  amount: number;
  currency: string;
  provider: string;
  status: number;
  row_version: number;
}

export function Payments() {
  const { token } = useAuth();
  const [attempts, setAttempts] = useState<Attempt[]>([]);
  const [error, setError] = useState('');
  const [org, setOrg] = useState('');
  const [amount, setAmount] = useState('');
  const [provider, setProvider] = useState('manual');

  const reload = () => {
    if (token) apiExt.paymentAttempts(token).then((a) => setAttempts(a as Attempt[])).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const intent = () => {
    if (!token) return;
    apiExt
      .paymentIntents(token, {
        org_id: Number(org),
        amount: Math.round(Number(amount) * 100),
        currency: 'USD',
        provider,
      })
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<CreditCard size={22} />} title="Payments" sub="Provider intents and attempts" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Attempts">
          <ul className="clean">
            {attempts.map((a) => (
              <li key={a.id}>
                <span>
                  {a.ref} — {(a.amount / 100).toFixed(2)} {a.currency} [{a.provider}]
                </span>
                <Badge tone={statusTone(a.status)}>
                  {a.status === 1 ? 'succeeded' : a.status === 0 ? 'pending' : a.status === -1 ? 'failed' : 'refunded'}
                </Badge>
              </li>
            ))}
          </ul>
        </Card>
        <Card title="New intent">
          <label className="field">
            Org ID <input value={org} onChange={(e) => setOrg(e.target.value)} />
          </label>
          <label className="field">
            Amount <input value={amount} onChange={(e) => setAmount(e.target.value)} />
          </label>
          <label className="field">
            Provider{' '}
            <select value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="manual">manual (settles at once)</option>
              <option value="stripe">stripe</option>
              <option value="paypal">paypal</option>
            </select>
          </label>
          <button className="primary" onClick={intent}>
            Start collection
          </button>
        </Card>
      </div>
    </div>
  );
}
