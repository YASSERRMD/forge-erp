import { useEffect, useState } from 'react';
import { api, apiExt, type Project, type Ticket } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';
import { Wrench } from 'lucide-react';

const projectStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Active',
  2: 'On hold',
  3: 'Closed',
  [-1]: 'Canceled',
};
const ticketStatus = ['Open', 'Pending', 'Resolved', 'Closed'];

interface Task {
  id: number;
  label: string;
  status: number;
  row_version: number;
}

interface TicketMsg {
  id: number;
  author: string;
  body: string;
  internal: boolean;
}

export function Services() {
  const { token, login } = useAuth();
  const [projects, setProjects] = useState<Project[]>([]);
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [error, setError] = useState('');
  const [pref, setPref] = useState('');
  const [plabel, setPlabel] = useState('');
  const [tref, setTref] = useState('');
  const [tsubject, setTsubject] = useState('');
  const [selProject, setSelProject] = useState<number | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [taskLabel, setTaskLabel] = useState('');
  const [projHours, setProjHours] = useState<number | null>(null);
  const [selTicket, setSelTicket] = useState<number | null>(null);
  const [messages, setMessages] = useState<TicketMsg[]>([]);
  const [msgBody, setMsgBody] = useState('');
  const [cref, setCref] = useState('');
  const [corg, setCorg] = useState('');
  const [clabel, setClabel] = useState('');
  const [iref, setIref] = useState('');
  const [iorg, setIorg] = useState('');
  const [ilabel, setIlabel] = useState('');

  const reload = () => {
    if (!token) return;
    api.projects(token).then(setProjects).catch(() => undefined);
    api.tickets(token).then(setTickets).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>, after?: () => void) =>
    fn.then(() => {
      setError('');
      reload();
      after?.();
    }).catch((e: Error) => setError(e.message));

  const loadTasks = (pid: number) => {
    if (!token) return;
    setSelProject(pid);
    apiExt.projectTasks(token, pid).then(setTasks).catch((e: Error) => setError(e.message));
    apiExt.projectHours(token, pid).then((h) => setProjHours(h.hours)).catch(() => undefined);
  };

  const loadMessages = (tid: number) => {
    if (!token) return;
    setSelTicket(tid);
    apiExt.ticketMessages(token, tid).then((m) => setMessages(m as TicketMsg[])).catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<Wrench size={22} />} title="Services" sub="Projects, tasks, time, contracts, interventions, tickets" />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title="Projects">
          <ul className="clean">
            {projects.map((p) => (
              <li key={p.id}>
                <button onClick={() => loadTasks(p.id)} title="Open tasks">
                  {p.ref}
                </button>
                <span>
                  {p.label} <Badge tone={statusTone(p.status)}>{projectStatus[p.status] ?? p.status}</Badge>
                </span>
                {p.status === 0 && token && (
                  <button onClick={() => act(api.setProjectStatus(token, p.id, 1, p.row_version))}>
                    Activate
                  </button>
                )}
                {(p.status === 1 || p.status === 2) && token && (
                  <button onClick={() => act(api.setProjectStatus(token, p.id, 3, p.row_version))}>
                    Close
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New project</h4>
          <label className="field">
            Ref <input value={pref} onChange={(e) => setPref(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={plabel} onChange={(e) => setPlabel(e.target.value)} />
          </label>
          <button className="primary" onClick={() => token && act(api.createProject(token, { ref: pref, label: plabel }))}>
            Create
          </button>
        </Card>
        <Card title={selProject ? `Tasks & time (project ${selProject})` : 'Tasks & time'}>
          {!selProject && <p className="muted">Select a project ref above.</p>}
          {selProject !== null && (
            <>
              <p className="muted">
                Booked: {projHours !== null ? `${(projHours / 100).toFixed(2)}h` : '—'}
              </p>
              <ul className="clean">
                {tasks.map((t) => (
                  <li key={t.id}>
                    <span>
                      {t.label} <Badge tone={statusTone(t.status)}>{t.status === 2 ? 'done' : t.status === 1 ? 'doing' : t.status === 0 ? 'todo' : 'canceled'}</Badge>
                    </span>
                    {t.status === 0 && token && selProject !== null && (
                      <button
                        onClick={() =>
                          act(apiExt.setTaskStatus(token, t.id, { status: 2, row_version: t.row_version }), () =>
                            loadTasks(selProject),
                          )
                        }
                      >
                        Finish
                      </button>
                    )}
                    {token && selProject !== null && (
                      <button
                        onClick={() => {
                          const h = prompt('Hours (decimal):', '1');
                          if (!h) return;
                          act(
                            apiExt.bookTime(token, t.id, {
                              project_id: selProject,
                              author: login ?? 'me',
                              hours: Math.round(Number(h) * 100),
                              entry_date: new Date().toISOString(),
                            }),
                            () => loadTasks(selProject),
                          );
                        }}
                      >
                        Book time
                      </button>
                    )}
                  </li>
                ))}
              </ul>
              <label className="field">
                New task <input value={taskLabel} onChange={(e) => setTaskLabel(e.target.value)} />
              </label>
              <button
                className="primary"
                onClick={() =>
                  token &&
                  selProject !== null &&
                  act(apiExt.createTask(token, selProject, { label: taskLabel }), () =>
                    loadTasks(selProject),
                  )
                }
              >
                Add
              </button>
            </>
          )}
        </Card>
      </div>
      <div className="grid two" style={{ marginTop: '1rem' }}>
        <Card title="Tickets">
          <ul className="clean">
            {tickets.map((t) => (
              <li key={t.id}>
                <button onClick={() => loadMessages(t.id)} title="Open thread">
                  {t.ref}
                </button>
                <span>
                  {t.subject} [P{t.priority}]{' '}
                  <Badge tone={statusTone(t.status)}>{ticketStatus[t.status] ?? t.status}</Badge>
                </span>
                {(t.status === 0 || t.status === 1) && token && (
                  <button onClick={() => act(api.setTicketStatus(token, t.id, 2, t.row_version))}>
                    Resolve
                  </button>
                )}
                {t.status === 2 && token && (
                  <button onClick={() => act(api.setTicketStatus(token, t.id, 3, t.row_version))}>
                    Close
                  </button>
                )}
                {t.status === 3 && token && (
                  <button onClick={() => act(api.setTicketStatus(token, t.id, 0, t.row_version))}>
                    Reopen
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>New ticket</h4>
          <label className="field">
            Ref <input value={tref} onChange={(e) => setTref(e.target.value)} />
          </label>
          <label className="field">
            Subject <input value={tsubject} onChange={(e) => setTsubject(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(api.createTicket(token, { ref: tref, subject: tsubject, priority: 2 }))}
          >
            Create
          </button>
          {selTicket !== null && (
            <>
              <h4>Thread</h4>
              <ul className="clean">
                {messages.map((m) => (
                  <li key={m.id}>
                    <span>
                      <strong>{m.author}:</strong> {m.body}
                    </span>
                  </li>
                ))}
              </ul>
              <label className="field">
                Reply <input value={msgBody} onChange={(e) => setMsgBody(e.target.value)} />
              </label>
              <button
                onClick={() =>
                  token &&
                  selTicket !== null &&
                  act(
                    apiExt.addTicketMessage(token, selTicket, { author: login ?? 'me', body: msgBody }),
                    () => loadMessages(selTicket),
                  )
                }
              >
                Send
              </button>
            </>
          )}
        </Card>
        <Card title="Contracts & interventions">
          <h4>New contract</h4>
          <label className="field">
            Ref <input value={cref} onChange={(e) => setCref(e.target.value)} />
          </label>
          <label className="field">
            Org ID <input value={corg} onChange={(e) => setCorg(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={clabel} onChange={(e) => setClabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(apiExt.createContract(token, { ref: cref, org_id: Number(corg), label: clabel }))
            }
          >
            Create
          </button>
          <h4>New intervention</h4>
          <label className="field">
            Ref <input value={iref} onChange={(e) => setIref(e.target.value)} />
          </label>
          <label className="field">
            Org ID <input value={iorg} onChange={(e) => setIorg(e.target.value)} />
          </label>
          <label className="field">
            Label <input value={ilabel} onChange={(e) => setIlabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(apiExt.createIntervention(token, { ref: iref, org_id: Number(iorg), label: ilabel }))
            }
          >
            Schedule
          </button>
        </Card>
      </div>
    </div>
  );
}
