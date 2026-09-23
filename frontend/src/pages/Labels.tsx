import { useEffect, useState } from 'react';
import { Printer } from 'lucide-react';
import { apiExt, type LabelSheet } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Card, PageHeader } from '../components/ui';

export function Labels() {
  const { token } = useAuth();
  const { t } = useLang();
  const [sheets, setSheets] = useState<LabelSheet[]>([]);
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [rows, setRows] = useState('8');
  const [cols, setCols] = useState('3');
  const [w, setW] = useState('70');
  const [h, setH] = useState('35');
  const [fields, setFields] = useState('sku,name');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (token) apiExt.labelSheets(token).then(setSheets).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const create = () => {
    if (!token) return;
    apiExt
      .createLabelSheet(token, {
        code,
        name,
        rows: Number(rows),
        cols: Number(cols),
        label_w_mm: Number(w),
        label_h_mm: Number(h),
        fields: fields.split(',').map((s) => s.trim()).filter(Boolean),
      })
      .then(() => {
        setError('');
        setNotice(t('created'));
        reload();
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };

  const remove = (id: number) => {
    if (!token) return;
    apiExt
      .deleteLabelSheet(token, id)
      .then(() => {
        setError('');
        reload();
      })
      .catch((e: Error) => setError(e.message));
  };

  return (
    <div>
      <PageHeader icon={<Printer size={22} />} title={t('labelSheets')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('sheetList')}>
        <ul className="clean">
          {sheets.map((s) => (
            <li key={s.id}>
              {s.code} — {s.name}{' '}
              <span className="muted">
                {s.rows}×{s.cols} {s.label_w_mm}×{s.label_h_mm}mm [{s.fields.join(', ')}]
              </span>{' '}
              <button onClick={() => remove(s.id)}>{t('delete')}</button>
            </li>
          ))}
        </ul>
        {sheets.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('newSheet')}>
        <label className="field">
          {t('code')} <input value={code} onChange={(e) => setCode(e.target.value)} />
        </label>
        <label className="field">
          {t('name')} <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="field">
          {t('rowsCols')}{' '}
          <input value={rows} onChange={(e) => setRows(e.target.value)} style={{ width: '4rem' }} /> ×{' '}
          <input value={cols} onChange={(e) => setCols(e.target.value)} style={{ width: '4rem' }} />
        </label>
        <label className="field">
          {t('labelSizeMm')}{' '}
          <input value={w} onChange={(e) => setW(e.target.value)} style={{ width: '4rem' }} /> ×{' '}
          <input value={h} onChange={(e) => setH(e.target.value)} style={{ width: '4rem' }} />
        </label>
        <label className="field">
          {t('fieldsCsv')} <input value={fields} onChange={(e) => setFields(e.target.value)} />
        </label>
        <button className="primary" onClick={create}>
          {t('create')}
        </button>
      </Card>
    </div>
  );
}
