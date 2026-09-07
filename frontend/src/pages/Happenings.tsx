import { useEffect, useState } from 'react';
import { CalendarRange } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

interface OrgEvent {
  id: number;
  title: string;
  capacity: number;
  status: number;
  row_version: number;
}

interface Position {
  id: number;
  code: string;
  title: string;
  status: number;
  row_version: number;
}

export function Happenings() {
  const { token } = useAuth();
  const [events, setEvents] = useState<OrgEvent[]>([]);
  const [positions, setPositions] = useState<Position[]>([]);
  const [error, setError] = useState('');
  const [etitle, setEtitle] = useState('');
  const [pcode, setPcode] = useState('');
  const [ptitle, setPtitle] = useState('');
  const [aname, setAname] = useState('');
  const [apos, setApos] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.orgEvents(token).then((e) => setEvents(e as OrgEvent[])).catch(() => undefined);
    apiExt.positions(token).then((p) => setPositions(p as Position[])).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  const now = new Date().toISOString();
  const tomorrow = new Date(Date.now() + 86400000).toISOString();

  return (
    <div>
      <PageHeader icon={<CalendarRange size={22} />} title="Events & hiring" sub="Organized events and open positions" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Events">
          <ul className="clean">
            {events.map((e) => (
              <li key={e.id}>
                <span>
                  {e.title} (cap {e.capacity})
                </span>
                <Badge tone={statusTone(e.status)}>{e.status === 1 ? 'published' : e.status === 0 ? 'draft' : 'done'}</Badge>
                {e.status === 0 && token && (
                  <button onClick={() => act(apiExt.setOrgEventStatus(token, e.id, { status: 1, row_version: e.row_version }))}>
                    Publish
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New event (tomorrow, cap 50)</h4>
          <label className="field">
            Title <input value={etitle} onChange={(e) => setEtitle(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(
                apiExt.createOrgEvent(token, {
                  title: etitle,
                  starts_at: now,
                  ends_at: tomorrow,
                  capacity: 50,
                }),
              )
            }
          >
            Create
          </button>
        </Card>
        <Card title="Hiring">
          <ul className="clean">
            {positions.map((p) => (
              <li key={p.id}>
                <span>
                  {p.code} — {p.title}
                </span>
                <Badge tone={statusTone(p.status)}>{p.status === 1 ? 'open' : p.status === 0 ? 'draft' : 'closed'}</Badge>
                {p.status === 0 && token && (
                  <button onClick={() => act(apiExt.setPositionStatus(token, p.id, { status: 1, row_version: p.row_version }))}>
                    Open
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New position</h4>
          <label className="field">
            Code <input value={pcode} onChange={(e) => setPcode(e.target.value)} />
          </label>
          <label className="field">
            Title <input value={ptitle} onChange={(e) => setPtitle(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(apiExt.createPosition(token, { code: pcode, title: ptitle }))}
          >
            Create
          </button>
          <h4>Apply</h4>
          <label className="field">
            Position ID <input value={apos} onChange={(e) => setApos(e.target.value)} />
          </label>
          <label className="field">
            Name <input value={aname} onChange={(e) => setAname(e.target.value)} />
          </label>
          <button
            onClick={() =>
              token && act(apiExt.applyToPosition(token, Number(apos), { name: aname }))
            }
          >
            Submit application
          </button>
        </Card>
      </div>
    </div>
  );
}
