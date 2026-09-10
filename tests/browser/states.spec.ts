import { expect, test } from '@playwright/test';

const healthy = {
  status: 'healthy', phase: 1,
  components: ['api', 'dependency', 'prometheus', 'toolbox'].map(name => ({ name, status: 'healthy' })),
};

test('binary terminal output, input, mobile resize and explicit reconnect', async ({ page }) => {
  const messages: { type: string; data?: string; cols?: number; rows?: number }[] = [];
  let connections = 0;
  let closures = 0;
  await page.route('**/api/status', route => route.fulfill({ json: healthy }));
  await page.routeWebSocket('**/terminal', socket => {
    connections++;
    socket.onClose(() => { closures++; });
    socket.onMessage(message => {
      const parsed = JSON.parse(message.toString());
      messages.push(parsed);
      if (parsed.type === 'input') socket.send(Buffer.from('BINARY_REPLY\r\n'));
    });
  });
  await page.goto('/#/exercises/first-investigation');
  expect(connections).toBe(0);
  await page.getByRole('button', { name: 'Connect terminal' }).click();
  await expect(page.getByTestId('terminal-status')).toHaveText('Connected');
  await expect.poll(() => messages.filter(message => message.type === 'resize').length).toBeGreaterThan(0);
  const initial = messages.filter(message => message.type === 'resize').at(-1)!;
  await page.getByRole('textbox', { name: 'Toolbox terminal input' }).focus();
  await page.keyboard.type('x');
  await expect(page.locator('.xterm-accessibility-tree')).toContainText('BINARY_REPLY');
  expect(messages).toContainEqual({ type: 'input', data: 'x' });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => messages.filter(message => message.type === 'resize').at(-1)?.cols).toBeLessThan(initial.cols!);
  expect(messages.filter(message => message.type === 'resize').at(-1)?.rows).toBeGreaterThan(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await expect(page.getByTestId('terminal-status')).toHaveText('Disconnected');
  await expect.poll(() => closures).toBe(1);
  await page.getByRole('button', { name: 'Connect terminal' }).click();
  await expect(page.getByTestId('terminal-status')).toHaveText('Connected');
  expect(connections).toBe(2);
  await expect(page.locator('.xterm-accessibility-tree')).not.toContainText('BINARY_REPLY');
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await expect.poll(() => closures).toBe(2);
});

test('startup is unknown, then live status can become degraded or unavailable', async ({ page }) => {
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let mode: 'healthy' | 'degraded' | 'unavailable' = 'healthy';
  await page.route('**/api/status', async route => {
    await gate;
    if (mode === 'unavailable') return route.fulfill({ status: 503, body: 'unavailable' });
    await route.fulfill({ json: mode === 'healthy' ? healthy : {
      ...healthy, status: 'degraded', components: healthy.components.map(component =>
        component.name === 'dependency' ? { ...component, status: 'unavailable' } : component),
    } });
  });
  await page.goto('/#/exercises/first-investigation');
  await expect(page.getByText('Checking system', { exact: true })).toBeVisible();
  await expect(page.getByText('unknown', { exact: true })).toHaveCount(4);
  await expect(page.getByText('System healthy', { exact: true })).toHaveCount(0);
  release();
  await expect(page.getByText('System healthy', { exact: true })).toBeVisible();
  mode = 'degraded';
  await expect(page.getByText('System degraded', { exact: true })).toBeVisible();
  await expect(page.getByText('unavailable', { exact: true })).toHaveCount(1);
  mode = 'unavailable';
  await expect(page.getByText('Status unavailable', { exact: true })).toBeVisible();
  await expect(page.getByText('unknown', { exact: true })).toHaveCount(4);
  await expect(page.getByRole('alert')).toContainText('HTTP 503');
});

test('incomplete health data never claims healthy and mobile has no page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/api/status', route => route.fulfill({ json: { ...healthy, components: [] } }));
  await page.goto('/#/exercises/first-investigation');
  await expect(page.getByText('Status unavailable', { exact: true })).toBeVisible();
  await expect(page.getByText('System healthy', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
});

test('unexpected terminal closure is explicit and allows another connection', async ({ page }) => {
  await page.route('**/api/status', route => route.fulfill({ json: healthy }));
  await page.routeWebSocket('**/terminal', socket => {
    socket.onMessage(() => socket.close({ code: 1011, reason: 'Toolbox unavailable' }));
  });
  await page.goto('/#/exercises/first-investigation');
  await page.getByRole('button', { name: 'Connect terminal' }).click();
  await expect(page.getByTestId('terminal-status')).toHaveText('Connection error');
  await expect(page.getByRole('alert')).toContainText('1011');
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toBeEnabled();
});
