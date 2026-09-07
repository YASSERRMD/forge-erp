import { useEffect, useState } from 'react';
import { Banknote } from 'lucide-react';
import { api, apiExt, type FinAccount } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Card, PageHeader } from '../components/ui';

export function Finance() {
  const { token } = useAuth();
  const [accounts, setAccounts] = useState<FinAccount[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [code, setCode] = useState('');
  const [label, setLabel] = useState('');
  const [type, setType] = useState('expense');
  const [jcode, setJcode] = useState('');
  const [jlabel, setJlabel] = useState('');
  const [bcode, setBcode] = useState('');
  const [blabel, setBlabel] = useState('');
  const [eJournal, setEJournal] = useState('');
  const [eDebit, setEDebit] = useState('');
  const [eCredit, setECredit] = useState('');
  const [eAmount, setEAmount] = useState('');
  const [eRef, setERef] = useState('');

  const reload = () => {
    if (token) api.accounts(token).then(setAccounts).catch(() => undefined);
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

  const postEntry = () => {
    if (!token) return;
    const amount = Math.round(Number(eAmount) * 100);
    act(
      apiExt.postEntry(token, {
        journal_id: Number(eJournal),
        ref: eRef,
        lines: [
          { account_id: Number(eDebit), debit: amount, credit: 0 },
          { account_id: Number(eCredit), debit: 0, credit: amount },
        ],
      }),
      'Entry posted',
    );
  };

  return (
    <div>
      <PageHeader icon={<Banknote size={22} />} title="Finance" sub="Chart, journals, entries, banking" />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="alert-ok">{notice}</p>}
      <div className="grid two">
        <Card title="Chart of accounts">
          <ul className="clean">
            {accounts.map((a) => (
              <li key={a.id}>
                <span>
                  {a.code} — {a.label} ({a.type})
                </span>
              </li>
            ))}
          </ul>
          <h4>New account</h4>
          <label className="field">
            Code <input value={code} onChange={(e) => setCode(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={label} onChange={(e) => setLabel(e.target.value)} />
          </label>
          <label className="field">
            Type{' '}
            <select value={type} onChange={(e) => setType(e.target.value)}>
              <option value="asset">asset</option>
              <option value="liability">liability</option>
              <option value="equity">equity</option>
              <option value="revenue">revenue</option>
              <option value="expense">expense</option>
            </select>
          </label>
          <button className="primary" onClick={() => token && act(api.createAccount(token, { code, label, type }), 'Account created')}>
            Create
          </button>
          <h4>New journal</h4>
          <label className="field">
            Code <input value={jcode} onChange={(e) => setJcode(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={jlabel} onChange={(e) => setJlabel(e.target.value)} />
          </label>
          <button
            onClick={() => token && act(apiExt.createJournal(token, { code: jcode, label: jlabel }), 'Journal created')}
          >
            Create
          </button>
          <h4>New bank account</h4>
          <label className="field">
            Code <input value={bcode} onChange={(e) => setBcode(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={blabel} onChange={(e) => setBlabel(e.target.value)} />
          </label>
          <button
            onClick={() => token && act(apiExt.createBankAccount(token, { code: bcode, label: blabel }), 'Bank account created')}
          >
            Create
          </button>
        </Card>
        <Card title="Post journal entry">
          <label className="field">
            Ref <input value={eRef} onChange={(e) => setERef(e.target.value)} />
          </label>
          <label className="field">
            Journal ID <input value={eJournal} onChange={(e) => setEJournal(e.target.value)} />
          </label>
          <label className="field">
            Debit acct <input value={eDebit} onChange={(e) => setEDebit(e.target.value)} />
          </label>
          <label className="field">
            Credit acct <input value={eCredit} onChange={(e) => setECredit(e.target.value)} />
          </label>
          <label className="field">
            Amount <input value={eAmount} onChange={(e) => setEAmount(e.target.value)} />
          </label>
          <button className="primary" onClick={postEntry}>
            Post balanced entry
          </button>
          <p className="muted">Debit and credit legs must balance to the cent.</p>
        </Card>
      </div>
    </div>
  );
}
