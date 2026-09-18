import { useEffect, useState } from 'react';
import { Tag } from 'lucide-react';
import { apiExt, type PriceRule } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, money } from '../components/ui';

export function Pricing() {
  const { token } = useAuth();
  const { t } = useLang();
  const [rules, setRules] = useState<PriceRule[]>([]);
  const [code, setCode] = useState('');
  const [label, setLabel] = useState('');
  const [expression, setExpression] = useState('max(base * 0.9, cost)');
  const [evalRule, setEvalRule] = useState('');
  const [base, setBase] = useState('1000');
  const [qty, setQty] = useState('1');
  const [cost, setCost] = useState('700');
  const [result, setResult] = useState<number | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.priceRules(token).then(setRules).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>, msg: string) =>
    fn.then(() => {
      setError('');
      setNotice(msg);
      reload();
    }).catch((e: Error) => {
      setError(e.message);
      setNotice('');
    });

  const evaluate = () => {
    if (!token || !evalRule) return;
    apiExt
      .evaluatePriceRule(token, Number(evalRule), {
        base: Number(base),
        qty: Number(qty),
        cost: Number(cost),
      })
      .then((r) => {
        setResult(r.price);
        setError('');
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<Tag size={22} />} title={t('priceRules')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('ruleList')}>
        <ul className="clean">
          {rules.map((r) => (
            <li key={r.id}>
              <span>
                {r.code} — <span className="muted">{r.expression}</span>
              </span>{' '}
              <Badge tone={r.status === 1 ? 'ok' : undefined}>
                {r.status === 1 ? t('validated') : t('draft')}
              </Badge>{' '}
              <button onClick={() => token && act(apiExt.deletePriceRule(token, r.id), t('deleted'))}>
                {t('cancel')}
              </button>
            </li>
          ))}
        </ul>
        {rules.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('newRule')}>
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
        <label className="field">
          {t('label')} <input value={label} onChange={(e) => setLabel(e.target.value)} />
        </label>
        <label className="field">
          {t('expression')} <input value={expression} onChange={(e) => setExpression(e.target.value)} />
        </label>
        <button
          className="primary"
          onClick={() => token && act(apiExt.createPriceRule(token, { code, label, expression }), t('created'))}
        >
          {t('create')}
        </button>
      </Card>
      <Card title={t('evaluate')}>
        <label className="field">
          {t('ruleList')}{' '}
          <select value={evalRule} onChange={(e) => setEvalRule(e.target.value)}>
            <option value="">—</option>
            {rules.map((r) => (
              <option key={r.id} value={r.id}>
                {r.code}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          {t('total')} (base) <input value={base} onChange={(e) => setBase(e.target.value)} />
        </label>
        <label className="field">
          {t('quantity')} <input value={qty} onChange={(e) => setQty(e.target.value)} />
        </label>
        <label className="field">
          {t('value')} (cost) <input value={cost} onChange={(e) => setCost(e.target.value)} />
        </label>
        <button className="primary" onClick={evaluate}>
          {t('evaluate')}
        </button>
        {result !== null && (
          <p>
            {t('result')}: <strong>{money(result)}</strong>
          </p>
        )}
      </Card>
    </div>
  );
}
