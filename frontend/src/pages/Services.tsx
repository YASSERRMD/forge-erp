import { useEffect, useState } from 'react';
import { api, apiExt, type Project, type Ticket } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';
import { Wrench } from 'lucide-react';

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

interface Contract {
  id: number;
  ref: string;
  label: string;
  status: number;
  row_version: number;
}

interface Intervention {
  id: number;
  ref: string;
  label: string;
  status: number;
  row_version: number;
}

const taskState = (s: number, t: (k: 'taskTodo' | 'taskDoing' | 'taskDone' | 'stCanceled') => string) =>
  s === 0 ? t('taskTodo') : s === 1 ? t('taskDoing') : s === 2 ? t('taskDone') : t('stCanceled');

export function Services() {
  const { token, login } = useAuth();
  const { t } = useLang();
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
  const [contracts, setContracts] = useState<Contract[]>([]);
  const [interventions, setInterventions] = useState<Intervention[]>([]);

  const projectStatus: Record<number, string> = {
    0: t('stDraft'),
    1: t('stActive'),
    2: t('stOnHold'),
    3: t('stClosed'),
    [-1]: t('stCanceled'),
  };
  const ticketStatus = [t('stOpen'), t('stPending'), t('stResolved'), t('stClosed')];

  const reload = () => {
    if (!token) return;
    api.projects(token).then(setProjects).catch(() => undefined);
    api.tickets(token).then(setTickets).catch(() => undefined);
    apiExt.contracts(token).then((c) => setContracts(c as Contract[])).catch(() => undefined);
    apiExt.interventions(token).then((x) => setInterventions(x as Intervention[])).catch(() => undefined);
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
      <PageHeader icon={<Wrench size={22} />} title={t('services')} />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title={t('services')}>
          <ul className="clean">
            {projects.map((p) => (
              <li key={p.id}>
                <button onClick={() => loadTasks(p.id)} title={t('details')}>
                  {p.ref}
                </button>
                <span>
                  {p.label} <Badge tone={statusTone(p.status)}>{projectStatus[p.status] ?? p.status}</Badge>
                </span>
                {p.status === 0 && token && (
                  <button onClick={() => act(api.setProjectStatus(token, p.id, 1, p.row_version))}>
                    {t('activate')}
                  </button>
                )}
                {(p.status === 1 || p.status === 2) && token && (
                  <button onClick={() => act(api.setProjectStatus(token, p.id, 3, p.row_version))}>
                    {t('close')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>{t('newProject')}</h4>
          <label className="field">
            {t('ref')} <input value={pref} onChange={(e) => setPref(e.target.value)} />
          </label>
          <label className="field">
            {t('label')} <input value={plabel} onChange={(e) => setPlabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(api.createProject(token, { ref: pref, label: plabel }))}
          >
            {t('create')}
          </button>
        </Card>
        <Card title={selProject ? `${t('tasks')} — ${t('project')} ${selProject}` : t('details')}>
          {!selProject && <p className="muted">{t('noData')}</p>}
          {selProject !== null && (
            <>
              <p className="muted">
                {t('hours')}: {projHours !== null ? `${(projHours / 100).toFixed(2)}h` : '—'}
              </p>
              <ul className="clean">
                {tasks.map((x) => (
                  <li key={x.id}>
                    <span>
                      {x.label} <Badge tone={statusTone(x.status)}>{taskState(x.status, t)}</Badge>
                    </span>
                    {x.status === 0 && token && selProject !== null && (
                      <button
                        onClick={() =>
                          act(apiExt.setTaskStatus(token, x.id, { status: 2, row_version: x.row_version }), () =>
                            loadTasks(selProject),
                          )
                        }
                      >
                        {t('close')}
                      </button>
                    )}
                    {token && selProject !== null && (
                      <button
                        onClick={() => {
                          const h = prompt(`${t('hours')} (decimal):`, '1');
                          if (!h) return;
                          act(
                            apiExt.bookTime(token, x.id, {
                              project_id: selProject,
                              author: login ?? 'me',
                              hours: Math.round(Number(h) * 100),
                              entry_date: new Date().toISOString(),
                            }),
                            () => loadTasks(selProject),
                          );
                        }}
                      >
                        {t('hours')}
                      </button>
                    )}
                  </li>
                ))}
              </ul>
              <label className="field">
                {t('newTask')}{' '}
                <input value={taskLabel} onChange={(e) => setTaskLabel(e.target.value)} />
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
                {t('add')}
              </button>
            </>
          )}
        </Card>
      </div>
      <div className="grid two" style={{ marginTop: '1rem' }}>
        <Card title={t('services')}>
          <ul className="clean">
            {tickets.map((x) => (
              <li key={x.id}>
                <button onClick={() => loadMessages(x.id)} title={t('details')}>
                  {x.ref}
                </button>
                <span>
                  {x.subject} [P{x.priority}]{' '}
                  <Badge tone={statusTone(x.status)}>{ticketStatus[x.status] ?? x.status}</Badge>
                </span>
                {(x.status === 0 || x.status === 1) && token && (
                  <button onClick={() => act(api.setTicketStatus(token, x.id, 2, x.row_version))}>
                    {t('resolve')}
                  </button>
                )}
                {x.status === 2 && token && (
                  <button onClick={() => act(api.setTicketStatus(token, x.id, 3, x.row_version))}>
                    {t('close')}
                  </button>
                )}
                {x.status === 3 && token && (
                  <button onClick={() => act(api.setTicketStatus(token, x.id, 0, x.row_version))}>
                    {t('reopen')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h4>{t('newTicket')}</h4>
          <label className="field">
            {t('ref')} <input value={tref} onChange={(e) => setTref(e.target.value)} />
          </label>
          <label className="field">
            {t('subject')} <input value={tsubject} onChange={(e) => setTsubject(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(api.createTicket(token, { ref: tref, subject: tsubject, priority: 2 }))}
          >
            {t('create')}
          </button>
          {selTicket !== null && (
            <>
              <h4>{t('thread')}</h4>
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
                {t('message')} <input value={msgBody} onChange={(e) => setMsgBody(e.target.value)} />
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
                {t('send')}
              </button>
            </>
          )}
        </Card>
        <Card title={t('services')}>
          <ul className="clean">
            {contracts.map((c) => (
              <li key={c.id}>
                <span>
                  {c.ref} — {c.label}{' '}
                  <Badge tone={statusTone(c.status)}>
                    {c.status === 1 ? t('stActive') : c.status === 0 ? t('stDraft') : c.status}
                  </Badge>
                </span>
              </li>
            ))}
          </ul>
          <h4>{t('newContract')}</h4>
          <label className="field">
            {t('ref')} <input value={cref} onChange={(e) => setCref(e.target.value)} />
          </label>
          <label className="field">
            {t('orgId')} <input value={corg} onChange={(e) => setCorg(e.target.value)} />
          </label>
          <label className="field">
            {t('label')} <input value={clabel} onChange={(e) => setClabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(apiExt.createContract(token, { ref: cref, org_id: Number(corg), label: clabel }))
            }
          >
            {t('create')}
          </button>
          <ul className="clean">
            {interventions.map((x) => (
              <li key={x.id}>
                <span>
                  {x.ref} — {x.label}{' '}
                  <Badge tone={statusTone(x.status)}>
                    {x.status === 0 ? t('stScheduled') : x.status === 1 ? t('stInProgress') : x.status === 2 ? t('stDone') : t('stCanceled')}
                  </Badge>
                </span>
              </li>
            ))}
          </ul>
          <h4>{t('newIntervention')}</h4>
          <label className="field">
            {t('ref')} <input value={iref} onChange={(e) => setIref(e.target.value)} />
          </label>
          <label className="field">
            {t('orgId')} <input value={iorg} onChange={(e) => setIorg(e.target.value)} />
          </label>
          <label className="field">
            {t('label')} <input value={ilabel} onChange={(e) => setIlabel(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token &&
              act(apiExt.createIntervention(token, { ref: iref, org_id: Number(iorg), label: ilabel }))
            }
          >
            {t('create')}
          </button>
        </Card>
      </div>
    </div>
  );
}
