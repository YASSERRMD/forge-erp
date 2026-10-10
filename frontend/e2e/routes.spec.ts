import { test } from '@playwright/test';
import { gotoClean, login } from './helpers';

// Every SPA route must render its page header with no error alert.
// This is the coverage gate for the whole frontend surface: a new page
// that crashes on load (bad hook, missing key, 500 on mount) fails here.
const ROUTES = [
  '/',
  '/organizations',
  '/products',
  '/sales',
  '/invoices',
  '/services',
  '/hr',
  '/pos',
  '/reports',
  '/manufacturing',
  '/finance',
  '/booking',
  '/documents',
  '/surveys',
  '/members',
  '/agenda',
  '/events',
  '/knowledge',
  '/suppliers',
  '/payments',
  '/collections',
  '/admin',
  '/transfers',
  '/pricing',
  '/dictionaries',
  '/mailing',
  '/partnerships',
  '/ai',
  '/data-policy',
  '/sepa',
  '/cron',
  '/fx',
  '/memos',
  '/bookmarks',
  '/collab',
  '/portal',
  '/website',
  '/ldap',
  '/labels',
  '/incoterms',
];
// NOTE: + /modules, /inbound, /search, /locales once PR #62 merges (batch 4).

test('all routes render without errors', async ({ page }) => {
  await login(page);
  for (const path of ROUTES) {
    await gotoClean(page, path);
  }
});
