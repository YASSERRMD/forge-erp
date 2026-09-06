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
  useEffect(() => {
    if (token) {
      api.projects(token).then(setProjects).catch(() => undefined);
      api.tickets(token).then(setTickets).catch(() => undefined);
    }
  }, [token]);
  return (
    <section>
      <h2>Services</h2>
      <h3>Projects</h3>
      <ul>
        {projects.map((p) => (
          <li key={p.id}>
            {p.ref} — {p.label} ({projectStatus[p.status] ?? p.status})
          </li>
        ))}
      </ul>
      <h3>Tickets</h3>
      <ul>
        {tickets.map((t) => (
          <li key={t.id}>
            {t.ref} — {t.subject} [P{t.priority}] ({ticketStatus[t.status] ?? t.status})
          </li>
        ))}
      </ul>
    </section>
  );
}
