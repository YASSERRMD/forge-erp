import { expect, test } from '@playwright/test';
import { login } from './helpers';

test('quote-to-cash seed: create a customer organization', async ({ page }) => {
  await login(page);
  const name = `E2E Customer ${Date.now()}`;
  await page.goto('/organizations');
  const card = page.locator('.card', { hasText: 'New customer' }).or(page.locator('.card').first());
  await card.locator('input').first().fill(name);
  await card.locator('input').nth(1).fill(`E2E${Date.now() % 100000}`);
  await card.locator('button.primary').click();
  await page.waitForLoadState('load');
  await expect(page.locator('li', { hasText: name }).first()).toBeVisible();
});

test('workspace: create a memo', async ({ page }) => {
  await login(page);
  const title = `E2E Memo ${Date.now()}`;
  await page.goto('/memos');
  const cards = page.locator('.card');
  await cards.nth(1).locator('input').fill(title);
  await cards.nth(1).locator('textarea').fill('written by playwright');
  const created = page.waitForResponse((r) => r.url().includes('/api/v1/memos') && r.request().method() === 'POST');
  await cards.nth(1).locator('button.primary').click();
  expect((await created).ok()).toBe(true);
  // Fresh mount re-fetches the list (the in-place reload can 429 under load).
  await page.reload();
  await expect(page.locator('.page-head h2')).toBeVisible();
  await expect(page.locator('li', { hasText: title }).first()).toBeVisible();
});

test('language toggle switches the nav to French', async ({ page }) => {
  await login(page);
  await page.getByRole('button', { name: 'FR', exact: true }).click();
  await expect(page.locator('.sidebar', { hasText: 'Tiers' })).toBeVisible();
  await page.getByRole('button', { name: 'EN', exact: true }).click();
  await expect(page.locator('.sidebar', { hasText: 'Organizations' })).toBeVisible();
});
