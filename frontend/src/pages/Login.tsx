import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import { useLang } from '../i18n/lang';

export function Login() {
  const { signIn } = useAuth();
  const { t } = useLang();
  const nav = useNavigate();
  const [login, setLogin] = useState('admin');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await signIn(login, password);
      nav('/');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed');
    }
  }

  return (
    <div className="login-wrap">
      <div className="card login-card">
        <h2>ForgeERP</h2>
        <p className="muted">Modern open-source ERP</p>
        <form onSubmit={submit}>
          <label className="field">
            {t('login')}
            <input value={login} onChange={(e) => setLogin(e.target.value)} autoComplete="username" />
          </label>
          <label className="field">
            {t('password')}
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </label>
          <button type="submit" className="primary">
            {t('signIn')}
          </button>
        </form>
        {error && (
          <p role="alert" className="alert-error">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}
