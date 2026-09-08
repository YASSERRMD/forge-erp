import { useEffect, useState } from 'react';
import { FileUp } from 'lucide-react';
import { apiExt } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

interface DocMeta {
  id: number;
  scope: string;
  object_id: number;
  name: string;
  size: number;
}

export function Documents() {
  const { token } = useAuth();
  const { t } = useLang();
  const [docs, setDocs] = useState<DocMeta[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [scope, setScope] = useState('general');
  const [file, setFile] = useState<File | null>(null);

  const reload = () => {
    if (token) apiExt.listDocuments(token).then((d) => setDocs(d as DocMeta[])).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const upload = () => {
    if (!token || !file) return;
    const form = new FormData();
    form.append('file', file);
    form.append('scope', scope);
    apiExt
      .uploadDocument(token, form)
      .then(() => {
        setNotice('Uploaded');
        setError('');
        reload();
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };

  return (
    <div>
      <PageHeader icon={<FileUp size={22} />} title={t('documents')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="alert-ok">{notice}</p>}
      <div className="grid two">
        <Card title={t('documents')}>
          <ul className="clean">
            {docs.map((d) => (
              <li key={d.id}>
                <span>
                  {d.name} — {d.scope} ({d.size} bytes)
                </span>
              </li>
            ))}
          </ul>
          {docs.length === 0 && <p className="muted">{t('noData')}</p>}
        </Card>
        <Card title={t('upload')}>
          <label className="field">
            {t('scope')} <input value={scope} onChange={(e) => setScope(e.target.value)} />
          </label>
          <label className="field">
            {t('file')} <input type="file" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          </label>
          <button className="primary" onClick={upload} disabled={!file}>
            {t('upload')}
          </button>
        </Card>
      </div>
    </div>
  );
}
