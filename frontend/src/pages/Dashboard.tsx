import { useEffect, useState } from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import {
  AlertCircle,
  Banknote,
  CalendarDays,
  FolderKanban,
  Receipt,
  TrendingDown,
  TrendingUp,
  Wallet,
} from 'lucide-react';
import { api, type AgendaEvent, type MonthlyPoint, type Project, type Ticket } from '../api/client';
import { useAuth } from '../auth/AuthContext';
import { Alert, Badge, Card, KPI, PageHeader, money, statusTone } from '../components/ui';
import { LayoutDashboard } from 'lucide-react';

export function Dashboard() {
  const { login, token } = useAuth();
  const [pnl, setPnl] = useState({ revenue: 0, expense: 0, net: 0 });
  const [receivable, setReceivable] = useState(0);
  const [monthly, setMonthly] = useState<MonthlyPoint[]>([]);
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [events, setEvents] = useState<AgendaEvent[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!token) return;
    const fail = (e: Error) => setError(e.message);
    api.pnl(token).then(setPnl).catch(fail);
    api.receivables(token).then((r) => setReceivable(r.total)).catch(fail);
    api.monthly(token).then(setMonthly).catch(fail);
    api.tickets(token).then(setTickets).catch(() => undefined);
    api.projects(token).then(setProjects).catch(() => undefined);
    const now = new Date();
    const from = new Date(now.getFullYear(), now.getMonth(), 1).toISOString();
    const to = new Date(now.getFullYear(), now.getMonth() + 2, 0).toISOString();
    api
      .agendaEvents(token, from, to)
      .then(setEvents)
      .catch(() => undefined);
  }, [token]);

  const openTickets = tickets.filter((t) => t.status === 0 || t.status === 1).length;
  const activeProjects = projects.filter((p) => p.status === 1).length;
  const donut = [
    { name: 'Revenue', value: Math.max(pnl.revenue, 0) },
    { name: 'Expense', value: Math.max(pnl.expense, 0) },
  ];

  return (
    <div>
      <PageHeader icon={<LayoutDashboard size={22} />} title={`Welcome, ${login}`} sub="Live operating picture across sales, finance and services" />
      {error && <Alert>{error}</Alert>}
      <div className="grid kpis">
        <KPI icon={<TrendingUp size={20} color="#fff" />} label="Revenue (posted)" value={money(pnl.revenue)} color="#dcfce7" />
        <KPI icon={<Wallet size={20} color="#fff" />} label="Outstanding receivables" value={money(receivable)} color="#dbeafe" />
        <KPI icon={<Banknote size={20} color="#fff" />} label="Net result" value={money(pnl.net)} color="#ede9fe" />
        <KPI icon={<AlertCircle size={20} color="#fff" />} label="Open tickets" value={String(openTickets)} color="#fef3c7" />
        <KPI icon={<FolderKanban size={20} color="#fff" />} label="Active projects" value={String(activeProjects)} color="#ffedd5" />
        <KPI icon={<CalendarDays size={20} color="#fff" />} label="Upcoming events" value={String(events.length)} color="#e0e7ff" />
      </div>
      <div className="grid two" style={{ marginTop: '1rem' }}>
        <Card title="Revenue by month (invoiced gross)">
          <ResponsiveContainer width="100%" height={260}>
            <BarChart data={monthly.map((m) => ({ ...m, grossMajor: m.gross / 100 }))}>
              <CartesianGrid strokeDasharray="3 3" stroke="#e3e8f2" />
              <XAxis dataKey="month" fontSize={11} />
              <YAxis fontSize={11} />
              <Tooltip formatter={(v) => [`${v}`, 'Gross']} />
              <Bar dataKey="grossMajor" fill="#2563eb" radius={[6, 6, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </Card>
        <Card title="Profit & loss mix">
          <ResponsiveContainer width="100%" height={260}>
            <PieChart>
              <Pie data={donut} dataKey="value" nameKey="name" innerRadius={60} outerRadius={95} paddingAngle={3}>
                <Cell fill="#2563eb" />
                <Cell fill="#f59e0b" />
              </Pie>
              <Tooltip formatter={(v) => [money(Number(v)), '']} />
            </PieChart>
          </ResponsiveContainer>
          <p className="muted">
            Expense ratio:{' '}
            {pnl.revenue > 0 ? `${((pnl.expense / pnl.revenue) * 100).toFixed(1)}%` : '—'}
          </p>
        </Card>
      </div>
      <div className="grid two" style={{ marginTop: '1rem' }}>
        <Card title="Needs attention (open tickets)">
          {tickets.filter((t) => t.status < 2).slice(0, 6).map((t) => (
            <div key={t.id} style={{ display: 'flex', gap: '0.5rem', padding: '0.3rem 0' }}>
              <Badge tone={t.priority >= 3 ? 'bad' : 'warn'}>P{t.priority}</Badge>
              <span>
                {t.ref} — {t.subject}
              </span>
            </div>
          ))}
          {openTickets === 0 && <p className="muted">Queue clear.</p>}
        </Card>
        <Card title="Upcoming events">
          {events.slice(0, 6).map((e) => (
            <div key={e.id} style={{ display: 'flex', gap: '0.5rem', padding: '0.3rem 0' }}>
              <Receipt size={15} />
              <span>
                {e.title} — {new Date(e.start_at).toLocaleDateString()}{' '}
                <Badge tone={statusTone(e.status)}>s{e.status}</Badge>
              </span>
            </div>
          ))}
          {events.length === 0 && <p className="muted">Nothing scheduled.</p>}
        </Card>
      </div>
      <p className="muted" style={{ marginTop: '1rem', display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
        <TrendingDown size={14} /> Figures stream live from /reports endpoints — no mocks.
      </p>
    </div>
  );
}
