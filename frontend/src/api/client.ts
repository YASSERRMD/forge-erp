// Typed API client for the ForgeERP REST surface (see api/openapi.yaml).
// Auth: dev JWT (localStorage) today; Keycloak OIDC bearer tomorrow — the
// Authorization header contract is identical, so only login() changes.

export interface LoginResponse {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_in: number;
}

export interface Organization {
  id: number;
  name: string;
  is_customer: boolean;
  is_supplier: boolean;
  customer_code: string;
  status: number;
}

export interface Product {
  id: number;
  sku: string;
  name: string;
  net_price: number;
  vat_rate_bps: number;
}

export interface SalesDocument {
  id: number;
  type: string;
  ref: string;
  status: number;
  totals: { net: number; vat: number; gross: number };
}

export interface Project {
  id: number;
  ref: string;
  label: string;
  status: number;
}

export interface Ticket {
  id: number;
  ref: string;
  subject: string;
  priority: number;
  status: number;
}

export interface LeaveRequest {
  id: number;
  user_login: string;
  type: string;
  days: number;
  status: number;
}

export interface ExpenseReport {
  id: number;
  ref: string;
  user_login: string;
  total: number;
  status: number;
}

const BASE = '';

export function authHeaders(token: string | null): Record<string, string> {
  const h: Record<string, string> = { 'Content-Type': 'application/json' };
  if (token) h['Authorization'] = `Bearer ${token}`;
  return h;
}

export function apiUrl(path: string): string {
  return `${BASE}${path.startsWith('/') ? path : `/${path}`}`;
}

async function request<T>(token: string | null, path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(apiUrl(path), {
    ...init,
    headers: { ...authHeaders(token), ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`API ${res.status}: ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  login: (login: string, password: string) =>
    request<LoginResponse>(null, '/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ login, password }),
    }),
  me: (token: string) => request<{ login: string }>(token, '/api/v1/auth/me'),
  organizations: (token: string) =>
    request<Organization[]>(token, '/api/v1/organizations?limit=50'),
  products: (token: string) => request<Product[]>(token, '/api/v1/products?limit=50'),
  invoices: (token: string) =>
    request<SalesDocument[]>(token, '/api/v1/sales/documents?type=invoice&limit=50'),
  search: (token: string, q: string) =>
    request<Array<{ scope: string; id: number; label: string; ref: string }>>(
      token,
      `/api/v1/search?q=${encodeURIComponent(q)}`,
    ),
  projects: (token: string) =>
    request<Project[]>(token, '/api/v1/services/projects?limit=50'),
  tickets: (token: string) =>
    request<Ticket[]>(token, '/api/v1/services/tickets?limit=50'),
  leaves: (token: string) =>
    request<LeaveRequest[]>(token, '/api/v1/hr/leaves?limit=50'),
  expenses: (token: string) =>
    request<ExpenseReport[]>(token, '/api/v1/hr/expenses?limit=50'),
};
