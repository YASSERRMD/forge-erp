import { useEffect, useState } from 'react';
import { Sparkles } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function AIAssist() {
  const { token } = useAuth();
  const { t } = useLang();
  const [prompt, setPrompt] = useState('');
  const [output, setOutput] = useState('');
  const [model, setModel] = useState('');
  const [runs, setRuns] = useState<Array<{ id: number; model: string; prompt_excerpt: string; output_excerpt: string }>>([]);
  const [error, setError] = useState('');

  const reload = () => {
    if (token) apiExt.aiRuns(token).then(setRuns).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const ask = () => {
    if (!token || !prompt.trim()) return;
    apiExt
      .assist(token, { prompt })
      .then((r) => {
        setOutput(r.output);
        setModel(r.model);
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<Sparkles size={22} />} title={t('aiAssist')} />
      {error && <Alert>{error}</Alert>}
      <Card title={t('askAI')}>
        <label className="field">
          {t('prompt')}{' '}
          <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={3} style={{ width: '100%' }} />
        </label>
        <button className="primary" onClick={ask}>
          {t('ask')}
        </button>
        {output && (
          <div>
            <h4>
              {t('result')} <span className="muted">({model})</span>
            </h4>
            <p>{output}</p>
          </div>
        )}
      </Card>
      <Card title={t('recentRuns')}>
        <ul className="clean">
          {runs.map((r) => (
            <li key={r.id}>
              <span className="muted">#{r.id} [{r.model}]</span> {r.prompt_excerpt} → {r.output_excerpt}
            </li>
          ))}
        </ul>
        {runs.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
    </div>
  );
}
