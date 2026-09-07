import { useEffect, useState } from 'react';
import { api, type Resource } from '../api/client';
import { useAuth } from '../auth/AuthContext';

export function Booking() {
  const { token } = useAuth();
  const [resources, setResources] = useState<Resource[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [resourceId, setResourceId] = useState('');
  const [user, setUser] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  useEffect(() => {
    if (token) api.resources(token).then(setResources).catch(() => undefined);
  }, [token]);
  const book = () => {
    if (!token) return;
    api
      .createBooking(token, {
        resource_id: Number(resourceId),
        user_login: user,
        start_at: new Date(start).toISOString(),
        end_at: new Date(end).toISOString(),
        seats: 1,
      })
      .then(() => {
        setNotice('Booked');
        setError('');
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };
  return (
    <section>
      <h2>Booking</h2>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      {notice && <p>{notice}</p>}
      <h3>Resources</h3>
      <ul>
        {resources.map((r) => (
          <li key={r.id}>
            {r.code} — {r.label} (cap {r.capacity})
          </li>
        ))}
      </ul>
      <h4>New booking</h4>
      <label>
        Resource ID{' '}
        <input value={resourceId} onChange={(e) => setResourceId(e.target.value)} />
      </label>{' '}
      <label>
        User <input value={user} onChange={(e) => setUser(e.target.value)} />
      </label>{' '}
      <label>
        Start <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
      </label>{' '}
      <label>
        End <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
      </label>{' '}
      <button onClick={book}>Book</button>
    </section>
  );
}
