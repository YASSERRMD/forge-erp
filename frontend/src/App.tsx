import { Link, Route, Routes, useNavigate } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth/AuthContext';
import { Login } from './pages/Login';
import { Dashboard, Invoices, Organizations, Products } from './pages/Entities';
import { Services } from './pages/Services';

function Shell() {
  const { token, login, signOut } = useAuth();
  const nav = useNavigate();
  if (!token) return <Login />;
  return (
    <>
      <nav style={{ display: 'flex', gap: '1rem', padding: '0.5rem 1rem' }}>
        <Link to="/">Dashboard</Link>
        <Link to="/organizations">Organizations</Link>
        <Link to="/products">Products</Link>
        <Link to="/invoices">Invoices</Link>
        <Link to="/services">Services</Link>
        <span style={{ marginLeft: 'auto' }}>{login}</span>
        <button
          onClick={() => {
            signOut();
            nav('/login');
          }}
        >
          Sign out
        </button>
      </nav>
      <main style={{ padding: '0 1rem' }}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/organizations" element={<Organizations />} />
          <Route path="/products" element={<Products />} />
          <Route path="/invoices" element={<Invoices />} />
          <Route path="/services" element={<Services />} />
        </Routes>
      </main>
    </>
  );
}

export function App() {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  );
}
