import { expect, test } from '@playwright/test';

test('learner discovery, touch explanations, and per-exercise notes', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: 'Exercise index', exact: true })).toBeFocused();
  await expect(page.getByRole('heading', { name: 'Build confidence troubleshooting real systems.' })).toBeVisible();
  await page.getByLabel('Experience level').selectOption('Beginner');
  await expect(page.locator('.exercise-card')).toHaveCount(6);
  await page.getByRole('searchbox').fill('forensics');
  await page.getByRole('link', { name: 'Open exercise', exact: true }).click();
  await expect(page.locator('.command-block')).toHaveCount(3);
  const explanation = page.getByText('Reveal explanation', { exact: true }).first();
  await explanation.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.reflection details').first()).toHaveAttribute('open', '');
  await page.getByRole('button', { name: 'Notes', exact: true }).click();
  await page.getByLabel('Investigation notes', { exact: true }).fill('PID and descriptor verified');
  await page.reload();
  await page.getByRole('button', { name: 'Notes', exact: true }).click();
  await expect(page.getByLabel('Investigation notes', { exact: true })).toHaveValue('PID and descriptor verified');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.goto('/#/exercises/connection-pool');
  await page.getByRole('button', { name: 'Notes', exact: true }).click();
  await expect(page.getByLabel('Investigation notes', { exact: true })).toHaveValue('');
});

test('copy commands and retain the terminal when switching tools', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('/#/exercises/first-investigation');
  await page.getByRole('button', { name: 'Copy command' }).first().click();
  await expect(page.locator('.command-block').first()).toContainText('Copied');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('curl');
  await page.getByRole('button', { name: 'Topology', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Connect terminal' })).toBeVisible();
});

test('runtime action is pending and self-check result is honestly labeled', async ({ page }) => {
  await page.route('**/api/run', route => route.fulfill({ json: { state: 'running', scenario: 'file-forensics', runID: 'ux-test' } }));
  await page.route('**/api/run/check', async route => {
    await new Promise(resolve => setTimeout(resolve, 500));
    await route.fulfill({ json: { passed: true } });
  });
  await page.goto('/#/exercises/file-forensics');
  const check = page.getByRole('button', { name: 'Review self-check' });
  await check.click();
  await expect(check).toBeDisabled();
  await expect(page.locator('.grade')).toContainText('Self-check recorded');
  await expect(page.locator('.grade')).toContainText('does not automatically inspect');
  await expect(page.locator('.completion-panel')).toContainText('CPU');
});
