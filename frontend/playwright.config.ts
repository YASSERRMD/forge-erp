import { defineConfig, devices } from '@playwright/test';

// E2E against the real stack: postgres + api boot via docker compose
// (FERP_ADMIN_PASSWORD set so the admin login is seeded), frontend served
// by `vite dev` (its /api proxy points at localhost:8080).
// Local: FERP_ADMIN_PASSWORD=... docker compose up -d postgres api
//        FERP_ADMIN_PASSWORD=... npm run test:e2e
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  // One worker: specs share the backend's per-IP rate-limit bucket, so
  // parallel contexts 429 each other. The API is the bottleneck anyway.
  workers: 1,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'on-first-retry',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'npm run dev -- --port 5173 --strictPort',
    url: 'http://localhost:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
