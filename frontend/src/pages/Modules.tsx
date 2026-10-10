import { useEffect, useState } from 'react';
import { Blocks } from 'lucide-react';
import { apiExt, type Activation, type ModuleInfo } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';
import { Alert, Badge, Card, PageHeader } from '../components/ui';

export function Modules() {
  const { token } = useAuth();
  const { t } = useLang();
  const [registry, setRegistry] = useState<ModuleInfo[]>([]);
  const [activations, setActivations] = useState<Activation[]>([]);
  const [name, setName] = useState('');
  const [family, setFamily] = useState('');
  const [scaffolded, setScaffolded] = useState<string[]>([]);
  const [manifestName, setManifestName] = useState('');
  const [manifestFamily, setManifestFamily] = useState('');
  const [version, setVersion] = useState('1.0.0');
  const [routesCsv, setRoutesCsv] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const reload = () => {
    if (!token) return;
    apiExt.modules(token).then(setRegistry).catch(() => undefined);
    apiExt.mbActivations(token).then(setActivations).catch(() => undefined);
  };
  useEffect(reload, [token]);

  const act = (fn: Promise<unknown>, msg: string) =>
    fn.then(() => {
      setError('');
      setNotice(msg);
      reload();
    }).catch((e: Error) => {
      setError(e.message);
      setNotice('');
    });

  const manifestOf = (n: string, f: string) => ({
    name: n,
    family: f,
    version,
    routes: routesCsv.split(',').map((s) => s.trim()).filter(Boolean),
    rights: [{ module: n, entity: '*', action: 'read' }],
  });

  const scaffold = () => {
    if (!token) return;
    apiExt
      .mbScaffold(token, { name, family })
      .then((r) => {
        setScaffolded(r.keys);
        setError('');
        setNotice(t('scaffolded'));
      })
      .catch((e: Error) => {
        setError(e.message);
        setNotice('');
      });
  };

  return (
    <div>
      <PageHeader icon={<Blocks size={22} />} title={t('modules')} />
      {error && <Alert>{error}</Alert>}
      {notice && <p className="muted">{notice}</p>}
      <Card title={t('registry')}>
        <ul className="clean">
          {registry.map((m) => (
            <li key={m.name}>
              {m.name} <span className="muted">({m.family})</span>{' '}
              <Badge tone={m.enabled ? 'ok' : 'warn'}>{m.enabled ? t('enabled') : t('disabled')}</Badge>{' '}
              <span className="muted">
                {m.rights.length} {t('rights')}
              </span>
            </li>
          ))}
        </ul>
        {registry.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('activations')}>
        <ul className="clean">
          {activations.map((a) => (
            <li key={a.name}>
              {a.name} <span className="muted">v{a.version}</span>{' '}
              <Badge tone={a.enabled ? 'ok' : undefined}>{a.enabled ? t('enabled') : t('disabled')}</Badge>{' '}
              <button
                onClick={() => token && act(apiExt.mbUninstall(token, { name: a.name, version: a.version }), t('uninstalled'))}
              >
                {t('uninstall')}
              </button>
            </li>
          ))}
        </ul>
        {activations.length === 0 && <p className="muted">{t('noData')}</p>}
      </Card>
      <Card title={t('scaffold')}>
        <label className="field">
          {t('name')} <input value={name} onChange={(e) => setName(e.target.value)} placeholder="myservice" />
        </label>
        <label className="field">
          {t('family')} <input value={family} onChange={(e) => setFamily(e.target.value)} placeholder="commerce" />
        </label>
        <button className="primary" onClick={scaffold}>
          {t('scaffold')}
        </button>
        {scaffolded.length > 0 && (
          <ul className="clean">
            {scaffolded.map((k) => (
              <li key={k}>{k}</li>
            ))}
          </ul>
        )}
      </Card>
      <Card title={t('validateInstall')}>
        <label className="field">
          {t('name')} <input value={manifestName} onChange={(e) => setManifestName(e.target.value)} />
        </label>
        <label className="field">
          {t('family')} <input value={manifestFamily} onChange={(e) => setManifestFamily(e.target.value)} />
        </label>
        <label className="field">
          {t('version')} <input value={version} onChange={(e) => setVersion(e.target.value)} />
        </label>
        <label className="field">
          {t('routesCsv')} <input value={routesCsv} onChange={(e) => setRoutesCsv(e.target.value)} />
        </label>
        <button
          onClick={() => token && act(apiExt.mbValidate(token, manifestOf(manifestName, manifestFamily)), t('valid'))}
        >
          {t('validate')}
        </button>{' '}
        <button
          className="primary"
          onClick={() => token && act(apiExt.mbInstall(token, manifestOf(manifestName, manifestFamily)), t('installed'))}
        >
          {t('install')}
        </button>
      </Card>
    </div>
  );
}
