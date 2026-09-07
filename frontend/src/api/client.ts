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
  row_version: number;
}

export interface Ticket {
  id: number;
  ref: string;
  subject: string;
  priority: number;
  status: number;
  row_version: number;
}

export interface LeaveRequest {
  id: number;
  user_login: string;
  type: string;
  days: number;
  status: number;
}

export interface Resource {
  id: number;
  code: string;
  label: string;
  capacity: number;
  status: number;
}

export interface BOM {
  id: number;
  ref: string;
  label: string;
  status: number;
}

export interface ManufacturingOrder {
  id: number;
  ref: string;
  qty: number;
  status: number;
  row_version: number;
}

export interface Survey {
  id: number;
  title: string;
  status: number;
  row_version: number;
}

export interface Tally {
  option_id: number;
  label: string;
  votes: number;
}

export interface FinAccount {
  id: number;
  code: string;
  label: string;
  type: string;
}

export interface ExpenseReport {
  id: number;
  ref: string;
  user_login: string;
  total: number;
  status: number;
}

export interface POSSale {
  id: number;
  ref: string;
  total_gross: number;
  change: number;
  status: number;
}

export interface SalesDocumentDetail extends SalesDocument {
  org_id: number;
}

export interface CheckoutLine {
  product_id: number;
  qty: number;
}

export interface CheckoutResult {
  id: number;
  ref: string;
  total_gross: number;
  change: number;
  invoice_id: number;
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
  sessionSales: (token: string, sessionId: number) =>
    request<POSSale[]>(token, `/api/v1/pos/sessions/${sessionId}/sales`),
  pnl: (token: string) =>
    request<{ revenue: number; expense: number; net: number }>(
      token,
      '/api/v1/reports/pnl',
    ),
  receivables: (token: string) =>
    request<{ rows: unknown[]; total: number }>(token, '/api/v1/reports/receivables'),
  checkout: (
    token: string,
    body: {
      session_id: number;
      org_id: number;
      lines: CheckoutLine[];
      method: string;
      tendered: number;
    },
  ) => request<CheckoutResult>(token, '/api/v1/pos/checkout', { method: 'POST', body: JSON.stringify(body) }),
  payInvoice: (
    token: string,
    body: { org_id: number; amount: number; currency: string; method: string; invoice_ids: number[] },
  ) => request<unknown>(token, '/api/v1/sales/payments', { method: 'POST', body: JSON.stringify(body) }),
  setDocumentStatus: (token: string, id: number, to: number) =>
    request<SalesDocument>(token, `/api/v1/sales/documents/${id}/status`, {
      method: 'POST',
      body: JSON.stringify({ to }),
    }),
  getDocument: (token: string, id: number) =>
    request<SalesDocumentDetail>(token, `/api/v1/sales/documents/${id}`),
  createOrganization: (token: string, body: { name: string; is_customer: boolean; customer_code: string }) =>
    request<Organization>(token, '/api/v1/organizations', { method: 'POST', body: JSON.stringify(body) }),
  createProduct: (
    token: string,
    body: { sku: string; name: string; type: number; unit: string; net_price: number; vat_rate_bps: number; stock_tracked: boolean },
  ) => request<Product>(token, '/api/v1/products', { method: 'POST', body: JSON.stringify(body) }),
  createProject: (token: string, body: { ref: string; label: string }) =>
    request<Project>(token, '/api/v1/services/projects', { method: 'POST', body: JSON.stringify(body) }),
  setProjectStatus: (token: string, id: number, status: number, row_version: number) =>
    request<Project>(token, `/api/v1/services/projects/${id}/status`, {
      method: 'POST',
      body: JSON.stringify({ status, row_version }),
    }),
  createTicket: (token: string, body: { ref: string; subject: string; priority: number }) =>
    request<Ticket>(token, '/api/v1/services/tickets', { method: 'POST', body: JSON.stringify(body) }),
  setTicketStatus: (token: string, id: number, status: number, row_version: number) =>
    request<Ticket>(token, `/api/v1/services/tickets/${id}/status`, {
      method: 'POST',
      body: JSON.stringify({ status, row_version }),
    }),
  resources: (token: string) => request<Resource[]>(token, '/api/v1/booking/resources'),
  createBooking: (
    token: string,
    body: { resource_id: number; user_login: string; start_at: string; end_at: string; seats: number },
  ) => request<unknown>(token, '/api/v1/bookings', { method: 'POST', body: JSON.stringify(body) }),
  cancelBooking: (token: string, id: number, row_version: number) =>
    request<unknown>(token, `/api/v1/bookings/${id}/status`, {
      method: 'POST',
      body: JSON.stringify({ status: -1, row_version }),
    }),
  boms: (token: string) => request<BOM[]>(token, '/api/v1/manufacturing/boms?limit=50'),
  mos: (token: string) => request<ManufacturingOrder[]>(token, '/api/v1/manufacturing/mos?limit=50'),
  produceMO: (token: string, id: number) =>
    request<unknown>(token, `/api/v1/manufacturing/mos/${id}/produce`, { method: 'POST' }),
  accounts: (token: string) => request<FinAccount[]>(token, '/api/v1/finance/accounts'),
  createAccount: (token: string, body: { code: string; label: string; type: string }) =>
    request<FinAccount>(token, '/api/v1/finance/accounts', { method: 'POST', body: JSON.stringify(body) }),
  surveys: (token: string) => request<Survey[]>(token, '/api/v1/surveys'),
  createSurvey: (token: string, body: { title: string }) =>
    request<Survey>(token, '/api/v1/surveys', { method: 'POST', body: JSON.stringify(body) }),
  setSurveyStatus: (token: string, id: number, status: number, row_version: number) =>
    request<Survey>(token, `/api/v1/surveys/${id}/status`, {
      method: 'POST',
      body: JSON.stringify({ status, row_version }),
    }),
  questionResults: (token: string, questionId: number) =>
    request<Tally[]>(token, `/api/v1/questions/${questionId}/results`),
};
