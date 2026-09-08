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
  row_version: number;
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

export interface MonthlyPoint {
  month: string;
  net: number;
  gross: number;
  count: number;
}

export interface Warehouse {
  id: number;
  code: string;
  label: string;
}

export interface AgendaEvent {
  id: number;
  title: string;
  start_at: string;
  end_at: string;
  status: number;
  row_version: number;
}

export interface Member {
  id: number;
  ref: string;
  first_name: string;
  last_name: string;
  company: string;
  status: number;
  row_version: number;
}

export interface Donation {
  id: number;
  ref: string;
  donor_name: string;
  amount: number;
  status: number;
  row_version: number;
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
  row_version: number;
}

export interface POSSale {
  id: number;
  ref: string;
  total_gross: number;
  change: number;
  status: number;
  lines: CheckoutLine[];
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
      payments?: Array<{ method: string; amount: number }>;
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
  monthly: (token: string) =>
    request<MonthlyPoint[]>(token, '/api/v1/reports/sales-monthly'),
  valuation: (token: string, warehouseId: number) =>
    request<{ rows: unknown[]; total: number }>(
      token,
      `/api/v1/reports/stock-valuation?warehouse_id=${warehouseId}`,
    ),
  intraEU: (token: string, home: string) =>
    request<Array<{ country: string; customers: number; net: number; vat: number }>>(
      token,
      `/api/v1/reports/intra-eu?home=${encodeURIComponent(home)}`,
    ),
  agendaEvents: (token: string, from: string, to: string) =>
    request<AgendaEvent[]>(
      token,
      `/api/v1/agenda/events?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&limit=50`,
    ),
};

// Full-surface coverage: every backend context reachable from the client.
// List/detail helpers return unknown[]/records where the UI only displays;
// write helpers take explicit bodies matching api/openapi.yaml.
function get<T>(token: string, path: string): Promise<T> {
  return request<T>(token, path);
}

function post<T>(token: string, path: string, body?: unknown): Promise<T> {
  return request<T>(token, path, {
    method: 'POST',
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export const apiExt = {
  // identity admin
  usersList: (t: string) => get<unknown[]>(t, '/api/v1/users?limit=50'),
  createUser: (t: string, body: unknown) => post<unknown>(t, '/api/v1/users', body),
  updateUser: (t: string, id: number, body: unknown) =>
    request<unknown>(t, `/api/v1/users/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  groups: (t: string) => get<unknown[]>(t, '/api/v1/groups?limit=50'),
  // sales workspace
  salesDocs: (t: string, type: string) =>
    get<SalesDocument[]>(t, `/api/v1/sales/documents?type=${type}&limit=50`),
  createSalesDoc: (t: string, body: unknown) =>
    post<SalesDocument>(t, '/api/v1/sales/documents', body),
  // catalog
  warehouses: (t: string) => get<Warehouse[]>(t, '/api/v1/warehouses?limit=50'),
  createWarehouse: (t: string, body: { code: string; label: string }) =>
    post<Warehouse>(t, '/api/v1/warehouses', body),
  stockLevels: (t: string, warehouseId: number, productId: number) =>
    get<unknown>(t, `/api/v1/stock-levels?warehouse_id=${warehouseId}&product_id=${productId}`),
  adjustStock: (t: string, body: unknown) => post<unknown>(t, '/api/v1/inventory-adjust', body),
  variants: (t: string, productId: number) =>
    get<unknown[]>(t, `/api/v1/products/${productId}/variants`),
  createVariant: (t: string, productId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/products/${productId}/variants`, body),
  // sales
  updateSalesDoc: (t: string, id: number, body: unknown) =>
    request<SalesDocument>(t, `/api/v1/sales/documents/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  convertDoc: (t: string, id: number, body: unknown) =>
    post<SalesDocument>(t, `/api/v1/sales/documents/${id}/convert`, body),
  fulfillShipment: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/sales/shipments/${id}/fulfill`, body),
  // procurement
  purchaseDocs: (t: string, type: string) =>
    get<unknown[]>(t, `/api/v1/purchase/documents?type=${type}&limit=50`),
  purchaseDoc: (t: string, id: number) => get<unknown>(t, `/api/v1/purchase/documents/${id}`),
  approvePurchase: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/purchase/documents/${id}/approve`, body),
  convertPurchase: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/purchase/documents/${id}/convert`, body),
  setPurchaseStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/purchase/documents/${id}/status`, body),
  recordSupplierPayment: (t: string, body: unknown) =>
    post<unknown>(t, '/api/v1/purchase/payments', body),
  pinSupplierPrice: (t: string, body: unknown) => post<unknown>(t, '/api/v1/purchase/prices', body),
  receiveReception: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/purchase/receptions/${id}/receive`, body),
  // finance
  journals: (t: string) => get<unknown[]>(t, '/api/v1/finance/journals'),
  createJournal: (t: string, body: unknown) => post<unknown>(t, '/api/v1/finance/journals', body),
  bankAccounts: (t: string) => get<unknown[]>(t, '/api/v1/finance/bank-accounts'),
  loans: (t: string) => get<unknown[]>(t, '/api/v1/finance/loans'),
  postEntry: (t: string, body: unknown) => post<unknown>(t, '/api/v1/finance/entries', body),
  trialBalance: (t: string) => get<unknown>(t, '/api/v1/finance/trial-balance'),
  chainVerify: (t: string) => get<unknown>(t, '/api/v1/finance/chain-verify'),
  createBankAccount: (t: string, body: unknown) => post<unknown>(t, '/api/v1/finance/bank-accounts', body),
  recordBankTx: (t: string, body: unknown) => post<unknown>(t, '/api/v1/finance/bank-transactions', body),
  reconcileBank: (t: string, id: number) =>
    post<unknown>(t, `/api/v1/finance/bank-transactions/${id}/reconcile`, {}),
  createLoan: (t: string, body: unknown) => post<unknown>(t, '/api/v1/finance/loans', body),
  // documents
  listDocuments: (t: string) => get<unknown[]>(t, '/api/v1/documents?limit=50'),
  uploadDocument: (t: string, form: FormData) =>
    fetch(apiUrl('/api/v1/documents/upload'), {
      method: 'POST',
      headers: t ? { Authorization: `Bearer ${t}` } : {},
      body: form,
    }).then(async (res) => {
      if (!res.ok) throw new Error(`API ${res.status}: ${await res.text()}`);
      return (await res.json()) as unknown;
    }),
  // hr
  createLeave: (t: string, body: unknown) => post<LeaveRequest>(t, '/api/v1/hr/leaves', body),
  setLeaveStatus: (t: string, id: number, body: unknown) =>
    post<LeaveRequest>(t, `/api/v1/hr/leaves/${id}/status`, body),
  createExpense: (t: string, body: unknown) => post<ExpenseReport>(t, '/api/v1/hr/expenses', body),
  addExpenseLine: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/hr/expenses/${id}/lines`, body),
  setExpenseStatus: (t: string, id: number, body: unknown) =>
    post<ExpenseReport>(t, `/api/v1/hr/expenses/${id}/status`, body),
  payExpense: (t: string, id: number, body: unknown) =>
    post<ExpenseReport>(t, `/api/v1/hr/expenses/${id}/pay`, body),
  createSalary: (t: string, body: unknown) => post<unknown>(t, '/api/v1/hr/salaries', body),
  salaries: (t: string) => get<unknown[]>(t, '/api/v1/hr/salaries?limit=50'),
  setSalaryStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/hr/salaries/${id}/status`, body),
  // pos
  terminals: (t: string) => get<unknown[]>(t, '/api/v1/pos/terminals'),
  createTerminal: (t: string, body: unknown) => post<unknown>(t, '/api/v1/pos/terminals', body),
  openSession: (t: string, body: unknown) => post<unknown>(t, '/api/v1/pos/sessions', body),
  closeSession: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/pos/sessions/${id}/close`, body),
  voidSale: (t: string, id: number) => post<unknown>(t, `/api/v1/pos/sales/${id}/void`, {}),
  posSale: (t: string, id: number) => get<POSSale>(t, `/api/v1/pos/sales/${id}`),
  returnSale: (t: string, saleId: number) =>
    post<{ sale: POSSale }>(t, '/api/v1/pos/returns', { sale_id: saleId }),
  // manufacturing
  createBOM: (t: string, body: unknown) => post<BOM>(t, '/api/v1/manufacturing/boms', body),
  bom: (t: string, id: number) => get<BOM>(t, `/api/v1/manufacturing/boms/${id}`),
  addBOMLine: (t: string, bomId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/manufacturing/boms/${bomId}/lines`, body),
  setBOMStatus: (t: string, id: number, body: unknown) =>
    post<BOM>(t, `/api/v1/manufacturing/boms/${id}/status`, body),
  createMO: (t: string, body: unknown) =>
    post<ManufacturingOrder>(t, '/api/v1/manufacturing/mos', body),
  setMOStatus: (t: string, id: number, body: unknown) =>
    post<ManufacturingOrder>(t, `/api/v1/manufacturing/mos/${id}/status`, body),
  // booking
  createResource: (t: string, body: unknown) => post<Resource>(t, '/api/v1/booking/resources', body),
  bookings: (t: string, resourceId: number, from: string, to: string) =>
    get<unknown[]>(
      t,
      `/api/v1/bookings?resource_id=${resourceId}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  // surveys
  createQuestion: (t: string, surveyId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/surveys/${surveyId}/questions`, body),
  addOption: (t: string, questionId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/questions/${questionId}/options`, body),
  castVote: (t: string, questionId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/questions/${questionId}/votes`, body),
  // agenda
  events: (t: string, from: string, to: string) =>
    get<AgendaEvent[]>(
      t,
      `/api/v1/agenda/events?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&limit=50`,
    ),
  createEvent: (t: string, body: unknown) => post<AgendaEvent>(t, '/api/v1/agenda/events', body),
  setEventStatus: (t: string, id: number, body: unknown) =>
    post<AgendaEvent>(t, `/api/v1/agenda/events/${id}/status`, body),
  dispatchReminders: (t: string) => post<{ dispatched: number }>(t, '/api/v1/agenda/reminders/dispatch', {}),
  // members
  memberTypes: (t: string) => get<unknown[]>(t, '/api/v1/member-types'),
  createMemberType: (t: string, body: unknown) => post<unknown>(t, '/api/v1/member-types', body),
  members: (t: string) => get<Member[]>(t, '/api/v1/members?limit=50'),
  createMember: (t: string, body: unknown) => post<Member>(t, '/api/v1/members', body),
  setMemberStatus: (t: string, id: number, body: unknown) =>
    post<Member>(t, `/api/v1/members/${id}/status`, body),
  createSubscription: (t: string, memberId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/members/${memberId}/subscriptions`, body),
  setSubscriptionStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/subscriptions/${id}/status`, body),
  donations: (t: string) => get<Donation[]>(t, '/api/v1/donations?limit=50'),
  createDonation: (t: string, body: unknown) => post<Donation>(t, '/api/v1/donations', body),
  setDonationStatus: (t: string, id: number, body: unknown) =>
    post<Donation>(t, `/api/v1/donations/${id}/status`, body),
  // sepa
  sepaBatches: (t: string) => get<unknown[]>(t, '/api/v1/sepa/batches'),
  createSepaBatch: (t: string, body: unknown) => post<unknown>(t, '/api/v1/sepa/batches', body),
  setSepaStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/sepa/batches/${id}/status`, body),
  sepaXMLUrl: (id: number) => apiUrl(`/api/v1/sepa/batches/${id}/xml`),
  // inbound
  mailboxes: (t: string) => get<unknown[]>(t, '/api/v1/inbound/mailboxes'),
  upsertMailbox: (t: string, body: unknown) => post<unknown>(t, '/api/v1/inbound/mailboxes', body),
  receiveMessage: (t: string, body: unknown) => post<unknown>(t, '/api/v1/inbound/messages', body),
  // kb + assets
  articles: (t: string, published: boolean) =>
    get<unknown[]>(t, `/api/v1/kb/articles?limit=50${published ? '&publishedOnly=1' : ''}`),
  createArticle: (t: string, body: unknown) => post<unknown>(t, '/api/v1/kb/articles', body),
  updateArticle: (t: string, id: number, body: unknown) =>
    request<unknown>(t, `/api/v1/kb/articles/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  setArticleStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/kb/articles/${id}/status`, body),
  kbSearch: (t: string, q: string) =>
    get<unknown[]>(t, `/api/v1/kb/search?q=${encodeURIComponent(q)}`),
  assets: (t: string) => get<unknown[]>(t, '/api/v1/assets?limit=50'),
  createAsset: (t: string, body: unknown) => post<unknown>(t, '/api/v1/assets', body),
  updateAsset: (t: string, id: number, body: unknown) =>
    request<unknown>(t, `/api/v1/assets/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  setAssetStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/assets/${id}/status`, body),
  // events org + hiring
  orgEvents: (t: string) => get<unknown[]>(t, '/api/v1/events'),
  createOrgEvent: (t: string, body: unknown) => post<unknown>(t, '/api/v1/events', body),
  setOrgEventStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/events/${id}/status`, body),
  registerAttendee: (t: string, eventId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/events/${eventId}/registrations`, body),
  positions: (t: string) => get<unknown[]>(t, '/api/v1/positions'),
  createPosition: (t: string, body: unknown) => post<unknown>(t, '/api/v1/positions', body),
  setPositionStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/positions/${id}/status`, body),
  applyToPosition: (t: string, positionId: number, body: unknown) =>
    post<unknown>(t, `/api/v1/positions/${positionId}/applications`, body),
  setApplicationStatus: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/applications/${id}/status`, body),
  // services extras
  projectHours: (t: string, id: number) =>
    get<{ hours: number }>(t, `/api/v1/services/projects/${id}/hours`),
  projectTasks: (t: string, projectId: number) =>
    get<Array<{ id: number; label: string; status: number; row_version: number }>>(
      t,
      `/api/v1/services/projects/${projectId}/tasks`,
    ),
  createTask: (t: string, projectId: number, body: { label: string }) =>
    post<unknown>(t, `/api/v1/services/projects/${projectId}/tasks`, body),
  setTaskStatus: (t: string, id: number, body: { status: number; row_version: number }) =>
    post<unknown>(t, `/api/v1/services/tasks/${id}/status`, body),
  bookTime: (
    t: string,
    taskId: number,
    body: { project_id: number; author: string; hours: number; entry_date: string },
  ) => post<unknown>(t, `/api/v1/services/tasks/${taskId}/time`, body),
  contracts: (t: string) => get<unknown[]>(t, '/api/v1/services/contracts?limit=50'),
  createContract: (t: string, body: unknown) => post<unknown>(t, '/api/v1/services/contracts', body),
  interventions: (t: string) => get<unknown[]>(t, '/api/v1/services/interventions?limit=50'),
  contractsOfOrg: (t: string, orgId: number) =>
    get<unknown[]>(t, `/api/v1/services/organizations/${orgId}/contracts`),
  setContractStatus: (t: string, id: number, body: { status: number; row_version: number }) =>
    post<unknown>(t, `/api/v1/services/contracts/${id}/status`, body),
  createIntervention: (t: string, body: unknown) =>
    post<unknown>(t, '/api/v1/services/interventions', body),
  setInterventionStatus: (t: string, id: number, body: { status: number; row_version: number }) =>
    post<unknown>(t, `/api/v1/services/interventions/${id}/status`, body),
  ticketMessages: (t: string, id: number) =>
    get<unknown[]>(t, `/api/v1/services/tickets/${id}/messages`),
  addTicketMessage: (t: string, id: number, body: unknown) =>
    post<unknown>(t, `/api/v1/services/tickets/${id}/messages`, body),
  // payments
  paymentIntents: (t: string, body: unknown) => post<unknown>(t, '/api/v1/payments/intents', body),
  paymentAttempts: (t: string) => get<unknown[]>(t, '/api/v1/payments/attempts?limit=50'),
  // dataio exports (browser download links)
  exportOrgsUrl: () => apiUrl('/api/v1/exports/organizations.csv'),
  exportProductsUrl: () => apiUrl('/api/v1/exports/products.csv'),
  // surveys depth
  surveyQuestions: (t: string, surveyId: number) =>
    get<Array<{ id: number; text: string; multi: boolean }>>(
      t,
      `/api/v1/surveys/${surveyId}/questions`,
    ),
  questionOptions: (t: string, questionId: number) =>
    get<Array<{ id: number; label: string }>>(t, `/api/v1/questions/${questionId}/options`),
  // manufacturing depth
  bomLines: (t: string, bomId: number) =>
    get<unknown[]>(t, `/api/v1/manufacturing/boms/${bomId}/lines`),
};
