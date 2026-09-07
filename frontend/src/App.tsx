import { Link, Route, Routes, useNavigate } from 'react-router-dom';
import { AuthProvider, useAuth } from './auth/AuthContext';
import { Login } from './pages/Login';
import { Dashboard, Invoices, Organizations, Products } from './pages/Entities';
import { Services } from './pages/Services';
import { HR } from './pages/HR';
import { POS } from './pages/POS';
import { Reports } from './pages/Reports';
import { Manufacturing } from './pages/Manufacturing';
import { Finance } from './pages/Finance';
import { Booking } from './pages/Booking';
import { Surveys } from './pages/Surveys';

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
        <Link to="/hr">HR</Link>
        <Link to="/pos">POS</Link>
        <Link to="/reports">Reports</Link>
        <Link to="/manufacturing">Manufacturing</Link>
        <Link to="/finance">Finance</Link>
        <Link to="/booking">Booking</Link>
        <Link to="/surveys">Surveys</Link>
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
          <Route path="/hr" element={<HR />} />
          <Route path="/pos" element={<POS />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/manufacturing" element={<Manufacturing />} />
          <Route path="/finance" element={<Finance />} />
          <Route path="/booking" element={<Booking />} />
          <Route path="/surveys" element={<Surveys />} />
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
