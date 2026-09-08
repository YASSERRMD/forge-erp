import { useEffect, useState } from 'react';
import { CalendarDays } from 'lucide-react';
import { api, apiExt, type AgendaEvent } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

export function Agenda() {
  const { token } = useAuth();
  const { t } = useLang();
  const [events, setEvents] = useState<AgendaEvent[]>([]);
  const [error, setError] = useState('');
  const [title, setTitle] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  const [dispatched, setDispatched] = useState<number | null>(null);

  const reload = () => {
    if (!token) return;
    const now = new Date();
    const from = new Date(now.getFullYear(), now.getMonth() - 1, 1).toISOString();
    const to = new Date(now.getFullYear(), now.getMonth() + 2, 0).toISOString();
    api.agendaEvents(token, from, to).then(setEvents).catch((e: Error) => setError(e.message));
  };
  useEffect(reload, [token]);

  const create = () => {
    if (!token || !title || !start || !end) return;
    apiExt
      .createEvent(token, {
        title,
        owner_login: 'me',
        start_at: new Date(start).toISOString(),
        end_at: new Date(end).toISOString(),
        reminder_min: 30,
      })
      .then(() => {
        setError('');
        setTitle('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<CalendarDays size={22} />} title={t('agenda')} />
      {error && <Alert>{error}</Alert>}
      <p>
        <button
          onClick={() =>
            token &&
            apiExt
              .dispatchReminders(token)
              .then((r) => setDispatched(r.dispatched))
              .catch((e: Error) => setError(e.message))
          }
        >
          {t('dispatch')}
        </button>{' '}
        {dispatched !== null && <span className="muted">{t('dispatched')}: {dispatched}</span>}
      </p>
      <Card title={t('upcoming')}>
        <ul className="clean">
          {events.map((e) => (
            <li key={e.id}>
              <span>
                {e.title} — {new Date(e.start_at).toLocaleString()}
              </span>
              <Badge tone={statusTone(e.status)}>
                {e.status === 0 ? t('stScheduled') : e.status === 1 ? t('stDone') : t('stCanceled')}
              </Badge>
            </li>
          ))}
        </ul>
        {events.length === 0 && <p className="muted">{t('noneScheduled')}</p>}
      </Card>
      <Card title={t('newEvent')}>
        <label className="field">
          {t('title')} <input value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="field">
          {t('start')} <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
        </label>
        <label className="field">
          {t('end')} <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
        </label>
        <button className="primary" onClick={create}>
          {t('schedule')}
        </button>
      </Card>
    </div>
  );
}
