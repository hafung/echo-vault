import { defineConfig } from '@playwright/test';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
const cached = join(homedir(), '.cache/ms-playwright/chromium-1228/chrome-linux64/chrome');
const chromium = process.env.ECHO_CHROMIUM || (existsSync(cached) ? cached : undefined);
export default defineConfig({ testDir: './tests', testMatch: '*.spec.ts', workers: 1, retries: 0, timeout: 45000, use: { baseURL: process.env.ECHO_TEST_URL || 'http://localhost:8765', browserName: 'chromium', launchOptions: { executablePath: chromium }, screenshot: 'only-on-failure', trace: 'retain-on-failure' }, reporter: 'list' });
