import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';

export function Login() {
  const { signIn } = useAuth();
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
    <main style={{ maxWidth: 360, margin: '4rem auto' }}>
      <h1>ForgeERP</h1>
      <form onSubmit={submit}>
        <label>
          Login
          <input value={login} onChange={(e) => setLogin(e.target.value)} autoComplete="username" />
        </label>
        <label>
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </label>
        <button type="submit">Sign in</button>
      </form>
      {error && <p role="alert">{error}</p>}
    </main>
  );
}
