import { expect, test } from '@playwright/test';

test('index supports discovery, clear availability and a guided workspace', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Exercise index', exact: true })).toBeVisible();
  await expect(page.locator('.exercise-card')).toHaveCount(14);
  await expect(page.getByText('Available walkthrough', { exact: true })).toHaveCount(1);
  await expect(page.getByText('Available exercise', { exact: true })).toHaveCount(13);
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toHaveCount(0);
  const search = page.getByRole('searchbox', { name: 'Find an exercise' });
  await search.fill('capacity');
  await expect(page.locator('.exercise-card')).toHaveCount(5);
  await expect(page.getByRole('heading', { name: 'CPU Saturation & Horizontal Scaling' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Start walkthrough' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Open exercise' })).toHaveCount(5);
  await search.fill('nonexistent topic');
  await expect(page.getByText('No exercises match this search.', { exact: false })).toBeVisible();
  await search.clear();
  await page.getByRole('link', { name: 'Start walkthrough' }).click();
  await expect(page).toHaveURL(/#\/exercises\/first-investigation$/);
  await expect(page.getByRole('heading', { name: 'Your objective' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'What to do' })).toBeVisible();
  await expect(page.getByText('there is no injected fault to fix', { exact: false })).toBeVisible();
  await expect(page.locator('.instructions pre')).toHaveCount(5);
  await expect(page.getByRole('checkbox')).toHaveCount(5);
  for (let index = 1; index <= 5; index++) {
    await page.getByRole('checkbox', { name: `I checked step ${index}'s result` }).check();
  }
  const answer = page.locator('.reflection details p').first();
  await expect(answer).toBeHidden();
  await page.getByText('Reveal explanation', { exact: true }).first().click();
  await expect(answer).toContainText('application-level evidence');
  await expect(page.getByText('Walkthrough self-check complete.', { exact: false })).toBeVisible();
  await page.getByRole('button', { name: 'Clear checklist' }).click();
  await expect(page.getByText('0 of 5 steps checked')).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Your First Investigation', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Exercise index', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Exercise index', exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Your First Investigation', exact: true })).toBeVisible();
});

test('incident exercise briefs can start a deterministic lab', async ({ page }) => {
  await page.goto('/#/exercises/cpu-saturation');
  await expect(page.getByText('Executable exercise.', { exact: false })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Exercise runbook' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start exercise' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toBeVisible();
  await expect(page.getByRole('checkbox')).toHaveCount(0);
  await expect(page.getByText('observed outcome', { exact: false }).first()).toBeVisible();
  await page.getByRole('button', { name: 'Start exercise' }).click();
  await expect(page.getByRole('button', { name: 'Reset exercise' })).toBeVisible({ timeout: 30000 });
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toBeVisible();
  await page.getByRole('button', { name: 'Reset exercise' }).click();
  await expect(page.getByRole('button', { name: 'Start exercise' })).toBeVisible({ timeout: 30000 });
});

test('unknown exercise is explicit and catalog fits a narrow viewport', async ({ page }) => {
  await page.goto('/#/exercises/not-a-real-exercise');
  await expect(page.getByRole('heading', { name: 'Exercise not available' })).toBeVisible();
  await page.getByRole('link', { name: 'Back to exercise index' }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await expect(page.getByRole('link', { name: 'Start walkthrough' })).toBeVisible();
});

test('each recommended exercise has a resource diagram and consultable tips', async ({ page }) => {
  for (const id of ['vertical-horizontal', 'dependency-bottleneck', 'connection-pool', 'latency-slo', 'dns-failure', 'retry-storm', 'memory-leak', 'autoscaler-oscillation', 'blocked-traffic']) {
    await page.goto(`/#/exercises/${id}`);
    await page.getByRole('button', { name: 'Topology', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Resource topology' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Hints' })).toBeVisible();
    await expect(page.getByText('Hint available', { exact: true }).first()).toBeVisible();
    await expect(page.getByRole('button', { name: 'Start exercise' })).toBeVisible();
  }
});
