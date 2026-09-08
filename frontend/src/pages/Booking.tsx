import { useEffect, useState } from 'react';
import { CalendarDays } from 'lucide-react';
import { api, apiExt, type Resource } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader, statusTone } from '../components/ui';

interface BookingItem {
  id: number;
  user_login: string;
  start_at: string;
  end_at: string;
  seats: number;
  status: number;
  row_version: number;
}

export function Booking() {
  const { token, login } = useAuth();
  const { t } = useLang();
  const [resources, setResources] = useState<Resource[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [rcode, setRcode] = useState('');
  const [rlabel, setRlabel] = useState('');
  const [rcap, setRcap] = useState('1');
  const [resourceId, setResourceId] = useState('');
  const [user, setUser] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  const [bookings, setBookings] = useState<BookingItem[]>([]);

  const reloadResources = () => {
    if (token) api.resources(token).then(setResources).catch(() => undefined);
  };
  useEffect(reloadResources, [token]);

  const act = (fn: Promise<unknown>, msg?: string) =>
    fn.then(() => {
      setNotice(msg ?? 'Done');
      setError('');
      reloadResources();
    }).catch((e: Error) => {
      setError(e.message);
      setNotice('');
    });

  const book = () => {
    if (!token) return;
    act(
      api.createBooking(token, {
        resource_id: Number(resourceId),
        user_login: user || (login ?? ''),
        start_at: new Date(start).toISOString(),
        end_at: new Date(end).toISOString(),
        seats: 1,
      }),
      'Booked',
    );
  };

  const viewBookings = () => {
    if (!token || !resourceId || !start || !end) return;
    apiExt
      .bookings(token, Number(resourceId), new Date(start).toISOString(), new Date(end).toISOString())
      .then((b) => setBookings(b as BookingItem[]))
      .catch((e: Error) => setError(e.message));
  };

  const bookingName = (s: number) =>
    s === 0 ? t('stBooked') : s === 1 ? t('stCheckedIn') : s === 2 ? t('stCompleted') : t('stCanceled');

  return (
    <div>
      <PageHeader icon={<CalendarDays size={22} />} title={t('booking')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="alert-ok">{notice}</p>}
      <div className="grid two">
        <Card title={t('booking')}>
          <ul className="clean">
            {resources.map((r) => (
              <li key={r.id}>
                <span>
                  {r.code} — {r.label} ({t('capacity')} {r.capacity})
                </span>
              </li>
            ))}
          </ul>
          {resources.length === 0 && <p className="muted">{t('noData')}</p>}
          <h4>{t('newResource')}</h4>
          <label className="field">
            {t('code')} <input value={rcode} onChange={(e) => setRcode(e.target.value)} />
          </label>
          <label className="field">
            {t('label')} <input value={rlabel} onChange={(e) => setRlabel(e.target.value)} />
          </label>
          <label className="field">
            {t('capacity')} <input value={rcap} onChange={(e) => setRcap(e.target.value)} />
          </label>
          <button
            className="primary"
            onClick={() =>
              token && act(apiExt.createResource(token, { code: rcode, label: rlabel, capacity: Number(rcap) }), t('create'))
            }
          >
            {t('create')}
          </button>
        </Card>
        <Card title={t('newBooking')}>
          <label className="field">
            {t('booking')} ID{' '}
            <input value={resourceId} onChange={(e) => setResourceId(e.target.value)} />
          </label>
          <label className="field">
            {t('user')} <input value={user} placeholder={login ?? ''} onChange={(e) => setUser(e.target.value)} />
          </label>
          <label className="field">
            {t('start')} <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
          </label>
          <label className="field">
            {t('end')} <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
          </label>
          <button className="primary" onClick={book}>
            {t('create')}
          </button>{' '}
          <button onClick={viewBookings}>{t('load')}</button>
          <ul className="clean">
            {bookings.map((b) => (
              <li key={b.id}>
                <span>
                  {b.user_login} — {new Date(b.start_at).toLocaleString()} ({b.seats} {t('seats')}){' '}
                  <Badge tone={statusTone(b.status)}>{bookingName(b.status)}</Badge>
                </span>
                {(b.status === 0 || b.status === 1) && token && (
                  <button onClick={() => act(api.cancelBooking(token, b.id, b.row_version), t('cancel'))}>
                    {t('cancel')}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </div>
  );
}
