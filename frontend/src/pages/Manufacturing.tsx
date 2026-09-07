import { useEffect, useState } from 'react';
import { api, type BOM, type ManufacturingOrder } from '../api/client';
import { useAuth } from '../auth/AuthContext';

const moStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Validated',
  2: 'In progress',
  3: 'Produced',
  [-1]: 'Canceled',
};

export function Manufacturing() {
  const { token } = useAuth();
  const [boms, setBoms] = useState<BOM[]>([]);
  const [mos, setMos] = useState<ManufacturingOrder[]>([]);
  const [error, setError] = useState('');
  const reload = () => {
    if (!token) return;
    api.boms(token).then(setBoms).catch(() => undefined);
    api.mos(token).then(setMos).catch(() => undefined);
  };
  useEffect(reload, [token]);
  const produce = (id: number) => {
    if (!token) return;
    api
      .produceMO(token, id)
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };
  return (
    <section>
      <h2>Manufacturing</h2>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      <h3>Bills of materials</h3>
      <ul>
        {boms.map((b) => (
          <li key={b.id}>
            {b.ref} — {b.label}
          </li>
        ))}
      </ul>
      <h3>Manufacturing orders</h3>
      <ul>
        {mos.map((m) => (
          <li key={m.id}>
            {m.ref} — qty {m.qty} ({moStatus[m.status] ?? m.status}){' '}
            {m.status === 2 && <button onClick={() => produce(m.id)}>Produce</button>}
          </li>
        ))}
      </ul>
    </section>
  );
}
