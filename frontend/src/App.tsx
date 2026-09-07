import { NavLink, Route, Routes, useNavigate } from 'react-router-dom';
import {
  Anvil,
  Banknote,
  BookOpen,
  BookOpenCheck,
  Building2,
  CalendarDays,
  CalendarRange,
  ClipboardList,
  CreditCard,
  Euro,
  FileText,
  FileUp,
  Factory,
  HeartHandshake,
  LayoutDashboard,
  LogOut,
  Package,
  Receipt,
  Settings2,
  ShieldCheck,
  ShoppingCart,
  Truck,
  Users,
  Wrench,
} from 'lucide-react';
import { AuthProvider, useAuth } from './auth/AuthContext';
import { LangProvider, useLang, type DictKey } from './i18n/lang';
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
import { Sales } from './pages/Sales';
import { Payments } from './pages/Payments';
import { Knowledge } from './pages/Knowledge';
import { Happenings } from './pages/Happenings';
import { Admin } from './pages/Admin';
import { Collections } from './pages/Collections';

interface NavItem {
  to: string;
  key: DictKey;
  icon: React.ReactNode;
}

const COMMERCE: NavItem[] = [
  { to: '/organizations', key: 'organizations', icon: <Building2 size={17} /> },
  { to: '/products', key: 'products', icon: <Package size={17} /> },
  { to: '/sales', key: 'sales', icon: <FileText size={17} /> },
  { to: '/invoices', key: 'invoices', icon: <Receipt size={17} /> },
  { to: '/pos', key: 'pos', icon: <ShoppingCart size={17} /> },
  { to: '/manufacturing', key: 'manufacturing', icon: <Factory size={17} /> },
];

const OPERATIONS: NavItem[] = [
  { to: '/services', key: 'services', icon: <Wrench size={17} /> },
  { to: '/hr', key: 'hr', icon: <Users size={17} /> },
  { to: '/booking', key: 'booking', icon: <CalendarDays size={17} /> },
  { to: '/documents', key: 'documents', icon: <FileUp size={17} /> },
  { to: '/agenda', key: 'agenda', icon: <ClipboardList size={17} /> },
  { to: '/surveys', key: 'surveys', icon: <BookOpenCheck size={17} /> },
  { to: '/members', key: 'members', icon: <HeartHandshake size={17} /> },
  { to: '/events', key: 'events', icon: <CalendarRange size={17} /> },
  { to: '/knowledge', key: 'knowledge', icon: <BookOpen size={17} /> },
];

const FINANCE: NavItem[] = [
  { to: '/finance', key: 'financeNav', icon: <Banknote size={17} /> },
  { to: '/reports', key: 'reports', icon: <Settings2 size={17} /> },
  { to: '/suppliers', key: 'suppliers', icon: <Truck size={17} /> },
  { to: '/payments', key: 'payments', icon: <CreditCard size={17} /> },
  { to: '/collections', key: 'collections', icon: <Euro size={17} /> },
  { to: '/admin', key: 'admin', icon: <ShieldCheck size={17} /> },
];

function Shell() {
  const { token, login, signOut } = useAuth();
  const { t, lang, setLang } = useLang();
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
            <LayoutDashboard size={17} /> {t('dashboard')}
          </NavLink>
          <div className="nav-section">{t('commerce')}</div>
          {COMMERCE.map((l) => (
            <NavLink key={l.to} to={l.to}>
              {l.icon} {t(l.key)}
            </NavLink>
          ))}
          <div className="nav-section">{t('operations')}</div>
          {OPERATIONS.map((l) => (
            <NavLink key={l.to} to={l.to}>
              {l.icon} {t(l.key)}
            </NavLink>
          ))}
          <div className="nav-section">{t('finance')}</div>
          {FINANCE.map((l) => (
            <NavLink key={l.to} to={l.to}>
              {l.icon} {t(l.key)}
            </NavLink>
          ))}
        </nav>
        <div className="side-foot">
          <div>{login}</div>
          <div style={{ display: 'flex', gap: '0.4rem', margin: '0.4rem 0' }}>
            <button onClick={() => setLang('en')} disabled={lang === 'en'}>
              EN
            </button>
            <button onClick={() => setLang('fr')} disabled={lang === 'fr'}>
              FR
            </button>
          </div>
          <button
            onClick={() => {
              signOut();
              nav('/login');
            }}
          >
            <LogOut size={14} style={{ verticalAlign: '-2px' }} /> {t('signOut')}
          </button>
        </div>
      </aside>
      <main className="main">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/organizations" element={<Organizations />} />
          <Route path="/products" element={<Products />} />
          <Route path="/sales" element={<Sales />} />
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
          <Route path="/events" element={<Happenings />} />
          <Route path="/knowledge" element={<Knowledge />} />
          <Route path="/suppliers" element={<Suppliers />} />
          <Route path="/payments" element={<Payments />} />
          <Route path="/collections" element={<Collections />} />
          <Route path="/admin" element={<Admin />} />
        </Routes>
      </main>
    </div>
  );
}

export function App() {
  return (
    <AuthProvider>
      <LangProvider>
        <Shell />
      </LangProvider>
    </AuthProvider>
  );
}
