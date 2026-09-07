import { NavLink, Route, Routes, useNavigate } from 'react-router-dom';
import {
  Anvil,
  Banknote,
  BookOpenCheck,
  Building2,
  CalendarDays,
  ClipboardList,
  FileUp,
  Factory,
  HeartHandshake,
  LayoutDashboard,
  LogOut,
  Package,
  Receipt,
  Settings2,
  ShoppingCart,
  Truck,
  Users,
  Wrench,
} from 'lucide-react';
import { AuthProvider, useAuth } from './auth/AuthContext';
import { Login } from './pages/Login';
import { Invoices, Organizations, Products } from './pages/Entities';
import { Dashboard } from './pages/Dashboard';
import { Services } from './pages/Services';
import { HR } from './pages/HR';
import { POS } from './pages/POS';
import { Reports } from './pages/Reports';
import { Manufacturing } from './pages/Manufacturing';
import { Finance } from './pages/Finance';
import { Booking } from './pages/Booking';
import { Surveys } from './pages/Surveys';
import { Members } from './pages/Members';
import { Agenda } from './pages/Agenda';
import { Suppliers } from './pages/Suppliers';
import { Documents } from './pages/Documents';

const COMMERCE = [
  { to: '/organizations', label: 'Organizations', icon: <Building2 size={17} /> },
  { to: '/products', label: 'Products', icon: <Package size={17} /> },
  { to: '/invoices', label: 'Invoices', icon: <Receipt size={17} /> },
  { to: '/pos', label: 'Point of sale', icon: <ShoppingCart size={17} /> },
  { to: '/manufacturing', label: 'Manufacturing', icon: <Factory size={17} /> },
];

const OPERATIONS = [
  { to: '/services', label: 'Services', icon: <Wrench size={17} /> },
  { to: '/hr', label: 'HR', icon: <Users size={17} /> },
  { to: '/booking', label: 'Booking', icon: <CalendarDays size={17} /> },
  { to: '/documents', label: 'Documents', icon: <FileUp size={17} /> },
  { to: '/agenda', label: 'Agenda', icon: <ClipboardList size={17} /> },
  { to: '/surveys', label: 'Surveys', icon: <BookOpenCheck size={17} /> },
  { to: '/members', label: 'Members', icon: <HeartHandshake size={17} /> },
];

const FINANCE = [
  { to: '/finance', label: 'Finance', icon: <Banknote size={17} /> },
  { to: '/reports', label: 'Reports', icon: <Settings2 size={17} /> },
  { to: '/suppliers', label: 'Suppliers', icon: <Truck size={17} /> },
];

function Shell() {
  const { token, login, signOut } = useAuth();
  const nav = useNavigate();
  if (!token) return <Login />;
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <Anvil size={22} /> ForgeERP
        </div>
        <nav className="nav">
          <NavLink to="/" end>
            <LayoutDashboard size={17} /> Dashboard
          </NavLink>
          <div className="nav-section">Commerce</div>
          {COMMERCE.map((l) => (
            <NavLink key={l.to} to={l.to}>
              {l.icon} {l.label}
            </NavLink>
          ))}
          <div className="nav-section">Operations</div>
          {OPERATIONS.map((l) => (
            <NavLink key={l.to} to={l.to}>
              {l.icon} {l.label}
            </NavLink>
          ))}
          <div className="nav-section">Finance</div>
          {FINANCE.map((l) => (
            <NavLink key={l.to} to={l.to}>
              {l.icon} {l.label}
            </NavLink>
          ))}
        </nav>
        <div className="side-foot">
          <div>{login}</div>
          <button
            onClick={() => {
              signOut();
              nav('/login');
            }}
          >
            <LogOut size={14} style={{ verticalAlign: '-2px' }} /> Sign out
          </button>
        </div>
      </aside>
      <main className="main">
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
          <Route path="/documents" element={<Documents />} />
          <Route path="/surveys" element={<Surveys />} />
          <Route path="/members" element={<Members />} />
          <Route path="/agenda" element={<Agenda />} />
          <Route path="/suppliers" element={<Suppliers />} />
        </Routes>
      </main>
    </div>
  );
}

export function App() {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  );
}
