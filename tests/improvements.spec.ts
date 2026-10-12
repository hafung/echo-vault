import { test, expect } from '@playwright/test';

test('project sidebar collapses, expands and remembers preference', async ({ page }) => {
  await page.goto('/');
  const toggle = page.locator('#sidebar-toggle');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  const before = await page.locator('.workspace').boundingBox();
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('.sidebar')).toBeHidden();
  const after = await page.locator('.workspace').boundingBox();
  expect(after!.width).toBeGreaterThan(before!.width + 150);
  await page.reload();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(page.locator('.sidebar')).toBeVisible();
  await expect(page.locator('.project-item').first()).toHaveCSS('font-size', '14px');
  await page.screenshot({ path: 'artifacts/improved-desktop.png' });
  await page.setViewportSize({width:390,height:844});
  await page.locator('#mobile-menu').click();
  await expect(page.locator('.brand')).toBeVisible();
  await page.screenshot({ path: 'artifacts/improved-mobile-sidebar.png' });
  await toggle.click();
  await expect(page.locator('body')).not.toHaveClass(/nav-open/);
});

test('backup gives immediate feedback, survives dialog close and offers a streaming download', async ({page}) => {
  let started = false, complete = false;
  const job = {id:'ui-backup-test',mode:'download',stage:'copying',started:Date.now()/1000,duration:2,done_bytes:3e9,total_bytes:7e9};
  await page.route('**/api/backup/status', route => route.fulfill({json:{directory:'/tmp/echo-backups',job: started ? {...job,stage:complete?'ready':'copying',done_bytes:complete?7e9:3e9} : null}}));
  await page.route('**/api/backup',async route=>{started=true;await new Promise(resolve=>setTimeout(resolve,800));await route.fulfill({json:job});});
  await page.route('**/api/backup/download?*',route=>route.fulfill({headers:{'content-type':'application/vnd.sqlite3','content-disposition':'attachment; filename="test.sqlite"'},body:'SQLite format 3\0test'}));
  await page.goto('/');
  await page.locator('#sources-button').click();
  await expect(page.locator('#backup-link')).toBeVisible();
  await page.locator('#backup-link').click();
  await expect(page.locator('#backup-progress')).toContainText('正在启动');
  await expect(page.locator('#backup-link')).toBeDisabled();
  await expect(page.locator('#backup-progress')).toContainText('42%');
  await page.locator('#sources-dialog [data-close-dialog]').click();
  await expect(page.locator('#backup-activity')).toBeVisible();
  const download = page.waitForEvent('download');
  complete = true;
  await download;
  await expect(page.locator('#backup-activity')).toBeHidden();
  await page.locator('#sources-button').click();
  await expect(page.locator('#backup-progress')).toContainText('完整备份已生成');
  await expect(page.locator('#backup-progress a')).toHaveAttribute('href','/api/backup/download?id=ui-backup-test');
});

test('local backup remembers directory and displays saved path, errors can be retried', async ({page})=>{
  let job: object|null = null, fail = false;
  let directory='/tmp/echo-backups';
  await page.route('**/api/backup/status',route=>route.fulfill({json:{directory,job}}));
  await page.route('**/api/backup/settings',route=>{directory=route.request().postDataJSON().directory;return route.fulfill({json:{directory}});});
  await page.route('**/api/backup',route=>{
    expect(route.request().postDataJSON().mode).toBe('local');
    job={id:fail?'failed':'local-test',mode:'local',stage:fail?'error':'ready',started:Date.now()/1000,duration:3,done_bytes:7e9,total_bytes:7e9,path:directory+'/echo-vault.sqlite',error:fail?'目标磁盘空间不足':''};
    return route.fulfill({json:job});
  });
  await page.goto('/');
  await page.locator('#sources-button').click();
  await page.locator('#backup-directory').fill('/tmp/我的备份');
  await page.getByRole('button',{name:'记住位置'}).click();
  await expect(page.locator('#toast')).toContainText('备份位置已记住');
  await page.locator('#backup-local').click();
  await expect(page.locator('#backup-progress')).toContainText('完整备份已保存');
  await expect(page.locator('#backup-progress code')).toHaveText('/tmp/我的备份/echo-vault.sqlite');
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:'artifacts/improved-mobile-backup.png'});
  fail=true;
  await page.locator('#backup-local').click();
  await expect(page.locator('#backup-progress')).toContainText('目标磁盘空间不足');
  await expect(page.locator('#backup-local')).toBeEnabled();
});
