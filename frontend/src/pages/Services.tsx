import { useEffect, useState } from 'react';
import { api, type Project, type Ticket } from '../api/client';
import { useAuth } from '../auth/AuthContext';

const projectStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Active',
  2: 'On hold',
  3: 'Closed',
  [-1]: 'Canceled',
};
const ticketStatus = ['Open', 'Pending', 'Resolved', 'Closed'];

export function Services() {
  const { token } = useAuth();
  const [projects, setProjects] = useState<Project[]>([]);
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [error, setError] = useState('');
  const [pref, setPref] = useState('');
  const [plabel, setPlabel] = useState('');
  const [tref, setTref] = useState('');
  const [tsubject, setTsubject] = useState('');

  const reload = () => {
    if (!token) return;
    api.projects(token).then(setProjects).catch(() => undefined);
    api.tickets(token).then(setTickets).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));

  return (
    <section>
      <h2>Services</h2>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      <h3>Projects</h3>
      <ul>
        {projects.map((p) => (
          <li key={p.id}>
            {p.ref} — {p.label} ({projectStatus[p.status] ?? p.status}){' '}
            {p.status === 0 && token && (
              <button onClick={() => act(api.setProjectStatus(token, p.id, 1, p.row_version))}>
                Activate
              </button>
            )}{' '}
            {(p.status === 1 || p.status === 2) && token && (
              <button onClick={() => act(api.setProjectStatus(token, p.id, 3, p.row_version))}>
                Close
              </button>
            )}
          </li>
        ))}
      </ul>
      <h4>New project</h4>
      <label>
        Ref <input value={pref} onChange={(e) => setPref(e.target.value)} />
      </label>{' '}
      <label>
        Label <input value={plabel} onChange={(e) => setPlabel(e.target.value)} />
      </label>{' '}
      <button
        onClick={() =>
          token && act(api.createProject(token, { ref: pref, label: plabel }))
        }
      >
        Create
      </button>
      <h3>Tickets</h3>
      <ul>
        {tickets.map((t) => (
          <li key={t.id}>
            {t.ref} — {t.subject} [P{t.priority}] ({ticketStatus[t.status] ?? t.status}){' '}
            {(t.status === 0 || t.status === 1) && token && (
              <button onClick={() => act(api.setTicketStatus(token, t.id, 2, t.row_version))}>
                Resolve
              </button>
            )}{' '}
            {t.status === 2 && token && (
              <button onClick={() => act(api.setTicketStatus(token, t.id, 3, t.row_version))}>
                Close
              </button>
            )}{' '}
            {t.status === 3 && token && (
              <button onClick={() => act(api.setTicketStatus(token, t.id, 0, t.row_version))}>
                Reopen
              </button>
            )}
          </li>
        ))}
      </ul>
      <h4>New ticket</h4>
      <label>
        Ref <input value={tref} onChange={(e) => setTref(e.target.value)} />
      </label>{' '}
      <label>
        Subject <input value={tsubject} onChange={(e) => setTsubject(e.target.value)} />
      </label>{' '}
      <button
        onClick={() =>
          token && act(api.createTicket(token, { ref: tref, subject: tsubject, priority: 2 }))
        }
      >
        Create
      </button>
    </section>
  );
}
