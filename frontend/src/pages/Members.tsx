import { useEffect, useState } from 'react';
import { Users } from 'lucide-react';
import { apiExt, type Donation, type Member } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, money, statusTone } from '../components/ui';

export function Members() {
  const { token } = useAuth();
  const [members, setMembers] = useState<Member[]>([]);
  const [donations, setDonations] = useState<Donation[]>([]);
  const [error, setError] = useState('');
  const [ref, setRef] = useState('');
  const [first, setFirst] = useState('');
  const [last, setLast] = useState('');

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

  return (
    <div>
      <PageHeader icon={<Users size={22} />} title="Members & donations" sub="Association membership and gift tracking" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Members">
          <ul className="clean">
            {members.map((m) => (
              <li key={m.id}>
                <span>
                  {m.ref} — {m.first_name} {m.last_name} {m.company}
                </span>
                <Badge tone={statusTone(m.status)}>
                  {m.status === 1 ? 'active' : m.status === 0 ? 'draft' : 'left'}
                </Badge>
                {m.status === 0 && token && (
                  <button onClick={() => act(apiExt.setMemberStatus(token, m.id, { status: 1, row_version: m.row_version }))}>
                    Validate
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New member</h4>
          <label className="field">
            Ref <input value={ref} onChange={(e) => setRef(e.target.value)} />
          </label>
          <label className="field">
            First <input value={first} onChange={(e) => setFirst(e.target.value)} />
          </label>
          <label className="field">
            Last <input value={last} onChange={(e) => setLast(e.target.value)} />
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
            Create
          </button>
          <p className="muted">Type defaults to ID 1 — manage classes via member-types API.</p>
        </Card>
        <Card title="Donations">
          <ul className="clean">
            {donations.map((d) => (
              <li key={d.id}>
                <span>
                  {d.ref} — {d.donor_name} — {money(d.amount)}
                </span>
                <Badge tone={statusTone(d.status)}>
                  {d.status === 1 ? 'paid' : d.status === 0 ? 'promised' : 'canceled'}
                </Badge>
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </div>
  );
}
