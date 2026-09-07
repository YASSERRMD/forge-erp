import type { ReactNode } from 'react';

export function Card({ title, action, children }: { title?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {(title || action) && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          {title && <h3>{title}</h3>}
          {action}
        </div>
      )}
      {children}
    </section>
  );
}

export function PageHeader({ icon, title, sub }: { icon: ReactNode; title: string; sub?: string }) {
  return (
    <div className="page-head">
      {icon}
      <div>
        <h2>{title}</h2>
        {sub && <div className="muted">{sub}</div>}
      </div>
    </div>
  );
}

export function KPI({
  icon,
  label,
  value,
  color,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  color: string;
}) {
  return (
    <div className="card kpi">
      <span className="icon" style={{ background: color }}>
        {icon}
      </span>
      <span>
        <span className="value">{value}</span>
        <br />
        <span className="label">{label}</span>
      </span>
    </div>
  );
}

export function Badge({ tone, children }: { tone?: 'ok' | 'warn' | 'bad'; children: ReactNode }) {
  return <span className={`badge${tone ? ` ${tone}` : ''}`}>{children}</span>;
}

export function Alert({ children }: { children: ReactNode }) {
  return <p className="alert-error">{children}</p>;
}

export function statusTone(status: number): 'ok' | 'warn' | 'bad' | undefined {
  if (status < 0) return 'bad';
  if (status === 0) return 'warn';
  return 'ok';
}

export function money(cents: number): string {
  return (cents / 100).toFixed(2);
}
