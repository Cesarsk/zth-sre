import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';

async function command(page: Page, text: string) {
  await page.getByRole('textbox', { name: 'Toolbox terminal input' }).focus();
  await page.keyboard.type(text);
  await page.keyboard.press('Enter');
}

test('live health, real PTY commands, resize, disconnect and fresh reconnect', async ({ page, request }) => {
  expect((await request.get('/healthz')).ok()).toBeTruthy();
  await expect.poll(async () => {
    const response = await request.get('/api/status');
    return response.ok() ? (await response.json()).status : 'unavailable';
  }, { timeout: 45000 }).toBe('healthy');
  const status = await (await request.get('/api/status')).json();
  expect(status.phase).toBe(1);
  expect(status.components).toHaveLength(4);
  for (const name of ['api', 'dependency', 'prometheus', 'toolbox']) {
    expect(status.components).toContainEqual({ name, status: 'healthy' });
  }

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Exercise index', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Start walkthrough' }).click();
  await expect(page.getByRole('heading', { name: 'SRE Lab', exact: true })).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'System healthy' })).toBeVisible();
  await expect(page.getByText('Exercises / Docker runtime')).toBeVisible();
  await expect(page.getByText('Guided self-check: investigate with real tools', { exact: false })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open Prometheus' })).toHaveAttribute('href', '/prometheus/');
  await expect(page.getByTestId('terminal-status')).toHaveText('Disconnected');

  const sizes: { cols: number; rows: number }[] = [];
  let binaryOutput = false;
  page.on('websocket', socket => {
    socket.on('framereceived', event => { binaryOutput ||= Buffer.isBuffer(event.payload); });
    socket.on('framesent', event => {
      const message = JSON.parse(event.payload.toString());
      if (message.type === 'resize') sizes.push(message);
    });
  });
  const socketPromise = page.waitForEvent('websocket', socket => new URL(socket.url()).pathname === '/terminal');
  await page.getByRole('button', { name: 'Connect terminal' }).click();
  const socket = await socketPromise;
  expect(new URL(socket.url()).host).toBe(new URL(page.url()).host);
  await expect(page.getByTestId('terminal-status')).toHaveText('Connected');
  const output = page.locator('.xterm-accessibility-tree');
  // The assembled marker cannot match the echoed command alone.
  const marker = randomUUID().slice(0, 8);
  await command(page, `printf 'SHELL_%s\\n' '${marker}'`);
  await expect(output).toContainText(`SHELL_${marker}`);
  expect(binaryOutput).toBeTruthy();
  await command(page, "curl -sS --max-time 10 -o /dev/null -w 'API_HTTP_%{http_code}\\n' http://api-lb:8080/");
  await expect(output).toContainText('API_HTTP_200');
  await command(page, "curl -fsS --max-time 10 http://api-1:8080/metrics | head -n 12");
  await expect(output).toContainText('# HELP');
  await expect(output).toContainText('# TYPE');
  await command(page, 'export SRE_BROWSER_SESSION=old');

  await expect.poll(() => sizes.length).toBeGreaterThan(0);
  const initial = sizes.at(-1)!;
  await page.setViewportSize({ width: 640, height: 850 });
  await expect.poll(() => sizes.at(-1)?.cols).toBeLessThan(initial.cols);
  const resized = sizes.at(-1)!;
  expect(resized.rows).toBeGreaterThan(0);
  await command(page, "printf 'PTY_SIZE_%s\\n' \"$(stty size)\"");
  await expect(output).toContainText(`PTY_SIZE_${resized.rows} ${resized.cols}`);

  const closed = new Promise<void>(resolve => socket.on('close', () => resolve()));
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await closed;
  await expect(page.getByTestId('terminal-status')).toHaveText('Disconnected');
  const reconnect = page.waitForEvent('websocket');
  await page.getByRole('button', { name: 'Connect terminal' }).click();
  const second = await reconnect;
  await expect(page.getByTestId('terminal-status')).toHaveText('Connected');
  await command(page, "printf 'SESSION_%s\\n' \"${SRE_BROWSER_SESSION:-fresh}\"");
  await expect(output).toContainText('SESSION_fresh');
  const cleanedUp = new Promise<void>(resolve => second.on('close', () => resolve()));
  await page.goto('about:blank');
  await cleanedUp;
});

test('Prometheus has scraped the real API and dependency', async ({ request }) => {
  await expect.poll(async () => {
    const response = await request.get('/prometheus/api/v1/query', { params: { query: 'up' } });
    if (!response.ok()) return [];
    const body = await response.json();
    if (body.status !== 'success') return [];
    return body.data.result
      .filter((sample: { metric: { instance?: string }; value: [number, string] }) => sample.value[1] === '1')
      .map((sample: { metric: { instance: string } }) => sample.metric.instance).sort();
  }, { timeout: 45000 }).toEqual(expect.arrayContaining(['api-1:8080', 'api-2:8080', 'dependency:8080']));
});

test('Toolbox is available immediately from an exercise and survives run startup', async ({ page }) => {
  await page.goto('/#/exercises/cpu-saturation');
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toBeVisible();
  const socketPromise = page.waitForEvent('websocket', socket => new URL(socket.url()).pathname === '/terminal');
  await page.getByRole('button', { name: 'Start exercise' }).click();
  await page.getByRole('button', { name: 'Connect terminal' }).click();
  await socketPromise;
  await expect(page.getByTestId('terminal-status')).toHaveText('Connected');
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await expect(page.getByTestId('terminal-status')).toHaveText('Disconnected');
  await expect(page.getByRole('button', { name: 'Reset exercise' })).toBeVisible({ timeout: 30000 });
  await page.getByRole('button', { name: 'Reset exercise' }).click();
});

test('the Prometheus UI renders through the same-origin proxy', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/prometheus/');
  await expect(page.getByRole('button', { name: 'Execute', exact: true })).toBeVisible();
  expect(errors).toEqual([]);
});

test('walkthrough self-check persists on the local lab server', async ({ page, request }) => {
  await page.goto('/#/exercises/first-investigation');
  await expect(page.getByText(/of 5 steps checked/)).toBeVisible();
  await page.getByRole('button', { name: 'Clear checklist' }).click();
  await expect.poll(async () => (await (await request.get('/api/progress/first-investigation')).json()).completedSteps).toEqual([]);
  await expect(page.getByText('0 of 5 steps checked')).toBeVisible();
  await page.getByRole('checkbox', { name: "I checked step 2's result" }).check();
  await expect.poll(async () => (await (await request.get('/api/progress/first-investigation')).json()).completedSteps).toEqual([1]);
  await page.getByRole('textbox', { name: 'Investigation notes' }).fill('The API response proves the dependency was called.');
  await expect.poll(async () => (await (await request.get('/api/progress/first-investigation')).json()).notes).toBe('The API response proves the dependency was called.');
  await page.reload();
  await expect(page.getByRole('checkbox', { name: "I checked step 2's result" })).toBeChecked();
  await expect(page.getByRole('textbox', { name: 'Investigation notes' })).toHaveValue('The API response proves the dependency was called.');
  const progress = await (await request.get('/api/progress/first-investigation')).json();
  expect(progress.completedSteps).toEqual([1]);
  expect(progress.notes).toBe('The API response proves the dependency was called.');
  await page.getByRole('button', { name: 'Clear checklist' }).click();
  await page.getByRole('textbox', { name: 'Investigation notes' }).fill('');
  await expect.poll(async () => await (await request.get('/api/progress/first-investigation')).json()).toMatchObject({ completedSteps: [], notes: '' });
});
