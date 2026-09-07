import { useEffect, useState } from 'react';
import { api, type Survey } from '../api/client';
import { useAuth } from '../auth/AuthContext';

const surveyStatus: Record<number, string> = {
  0: 'Draft',
  1: 'Open',
  2: 'Closed',
};

export function Surveys() {
  const { token } = useAuth();
  const [surveys, setSurveys] = useState<Survey[]>([]);
  const [error, setError] = useState('');
  const [title, setTitle] = useState('');
  const reload = () => {
    if (token) api.surveys(token).then(setSurveys).catch(() => undefined);
  };
  useEffect(reload, [token]);
  const act = (fn: Promise<unknown>) =>
    fn.then(() => {
      setError('');
      reload();
    }).catch((e: Error) => setError(e.message));
  return (
    <section>
      <h2>Surveys</h2>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      <ul>
        {surveys.map((s) => (
          <li key={s.id}>
            {s.title} ({surveyStatus[s.status] ?? s.status}){' '}
            {s.status === 0 && token && (
              <button onClick={() => act(api.setSurveyStatus(token, s.id, 1, s.row_version))}>
                Open
              </button>
            )}{' '}
            {s.status === 1 && token && (
              <button onClick={() => act(api.setSurveyStatus(token, s.id, 2, s.row_version))}>
                Close
              </button>
            )}
          </li>
        ))}
      </ul>
      <h4>New survey</h4>
      <label>
        Title <input value={title} onChange={(e) => setTitle(e.target.value)} />
      </label>{' '}
      <button onClick={() => token && act(api.createSurvey(token, { title }))}>
        Create
      </button>
    </section>
  );
}
