import { expect, test } from '@playwright/test';
import { login } from './helpers';

test('rejects a wrong password', async ({ page }) => {
  await page.goto('/login');
  await page.locator('.login-card input[type="password"]').fill('definitely-wrong');
  await page.locator('.login-card button.primary').click();
  await expect(page.locator('.login-card [role="alert"]')).toBeVisible();
  await expect(page).toHaveURL(/login/);
});

test('admin login lands on the dashboard', async ({ page }) => {
  await login(page);
  await expect(page.locator('.page-head h2')).toBeVisible();
});
