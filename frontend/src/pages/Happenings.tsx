import { useEffect, useState } from 'react';
import { CalendarRange } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
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
  const { t } = useLang();
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
      <PageHeader icon={<CalendarRange size={22} />} title={t('events')} />
      {error && <Alert>{error}</Alert>}
      <div className="grid two">
        <Card title={t('events')}>
          <ul className="clean">
            {events.map((e) => (
              <li key={e.id}>
                <span>
                  {e.title} ({t('capacity')} {e.capacity})
                </span>
                <Badge tone={statusTone(e.status)}>{e.status === 1 ? t('stPublished') : e.status === 0 ? t('stDraft') : t('stDone')}</Badge>
                {e.status === 0 && token && (
                  <button onClick={() => act(apiExt.setOrgEventStatus(token, e.id, { status: 1, row_version: e.row_version }))}>
                    {t('publish')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {events.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newEvent')}</h4>
          <label className="field">
            {t('title')} <input value={etitle} onChange={(e) => setEtitle(e.target.value)} />
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
            {t('create')}
          </button>
        </Card>
        <Card title={t('events')}>
          <ul className="clean">
            {positions.map((p) => (
              <li key={p.id}>
                <span>
                  {p.code} — {p.title}
                </span>
                <Badge tone={statusTone(p.status)}>{p.status === 1 ? t('stOpen') : p.status === 0 ? t('stDraft') : t('stClosed')}</Badge>
                {p.status === 0 && token && (
                  <button onClick={() => act(apiExt.setPositionStatus(token, p.id, { status: 1, row_version: p.row_version }))}>
                    {t('open')}
                  </button>
                )}
              </li>
            ))}
          </ul>
          {positions.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newPosition')}</h4>
          <label className="field">
            {t('code')} <input value={pcode} onChange={(e) => setPcode(e.target.value)} />
          </label>
          <label className="field">
            {t('title')} <input value={ptitle} onChange={(e) => setPtitle(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() => token && act(apiExt.createPosition(token, { code: pcode, title: ptitle }))}
          >
            {t('create')}
          </button>
          <h4>{t('newPosition')}</h4>
          <label className="field">
            Position ID <input value={apos} onChange={(e) => setApos(e.target.value)} />
          </label>
          <label className="field">
            {t('name')} <input value={aname} onChange={(e) => setAname(e.target.value)} />
          </label>
          <button
            onClick={() =>
              token && act(apiExt.applyToPosition(token, Number(apos), { name: aname }))
            }
          >
            {t('submit')}
          </button>
        </Card>
      </div>
    </div>
  );
}
