import type { Page } from '@playwright/test';
import { expect } from '@playwright/test';

export function adminPassword(): string {
  const pw = process.env.FERP_ADMIN_PASSWORD;
  if (!pw) throw new Error('FERP_ADMIN_PASSWORD must be set (backend seeds admin from it)');
  return pw;
}

// Logs in via the UI (login field defaults to "admin") and waits for the shell.
export async function login(page: Page): Promise<void> {
  await page.goto('/login');
  await page.locator('.login-card input[type="password"]').fill(adminPassword());
  await page.locator('.login-card button.primary').click();
  await page.waitForURL('/');
  await page.locator('.sidebar').waitFor();
}

// Navigates and asserts a clean render. The API rate-limits at ~20 req/s,
// so a fast loop can 429: on a 429 alert, wait for a refill and retry once.
export async function gotoClean(page: Page, path: string): Promise<void> {
  await page.goto(path);
  await expect(page.locator('.page-head h2'), `header missing on ${path}`).toBeVisible();
  const alert = page.locator('.alert-error');
  if ((await alert.count()) > 0 && (await alert.first().textContent())?.includes('429')) {
    await page.waitForTimeout(3000);
    await page.reload();
    await expect(page.locator('.page-head h2'), `header missing on ${path} (retry)`).toBeVisible();
  }
  await expect(page.locator('.alert-error'), `error alert on ${path}`).toHaveCount(0);
  // Pace the loop so the token bucket keeps up (~4 pages/s max).
  await page.waitForTimeout(250);
}
