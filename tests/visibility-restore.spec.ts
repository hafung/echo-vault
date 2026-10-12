import { test, expect } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  const sessions = [
    {id:'recent',title:'最近项目的共同关键词',project:'最近项目',updated:300,hidden:0,project_hidden:0},
    {id:'old',title:'旧项目的共同关键词',project:'旧项目',updated:100,hidden:0,project_hidden:0},
    {id:'archived',title:'归档对话的共同关键词',project:'旧项目',updated:90,hidden:1,project_hidden:0},
  ].map(s=>({...s,created:1,provider:'Codex',platform:'WSL',favorite:0,archived:0,model:'',message_count:1,total_count:1,preview:'共同关键词',matches:0}));
  await page.route('**/api/stats?*',route=>{
    const showHidden=new URL(route.request().url()).searchParams.get('hidden')==='1';
    const items=sessions.filter(s=>showHidden||!s.hidden&&!s.project_hidden);
    const projects=[...new Set(items.map(s=>s.project))].map(project=>({project,count:items.filter(s=>s.project===project).length,updated:Math.max(...items.filter(s=>s.project===project).map(s=>s.updated)),hidden:Number(sessions.some(s=>s.project===project&&s.project_hidden))})).sort((a,b)=>b.updated-a.updated);
    return route.fulfill({json:{sessions:items.length,projects:projects.length,projects_list:projects,favorites:0,messages:items.length,db_bytes:1024,platforms:[],providers:[],last_sync:0}});
  });
  await page.route('**/api/sessions?*',route=>{
    const p=new URL(route.request().url()).searchParams;
    const items=sessions.filter(s=>(p.get('hidden')==='1'||!s.hidden&&!s.project_hidden)&&(!p.get('project')||s.project===p.get('project')));
    return route.fulfill({json:{items,more:false,offset:0}});
  });
  await page.route(/\/api\/sessions\/(recent|old|archived)\?/,route=>{
    const id=new URL(route.request().url()).pathname.split('/')[3];
    return route.fulfill({json:{session:sessions.find(s=>s.id===id),items:[{id:1,role:'user',kind:'message',phase:'',timestamp:1,body:'共同关键词',metadata:{},length:5,truncated:false}],offset:0,total:1,more:false}});
  });
  await page.route('**/api/sessions/*/visibility',route=>{
    const id=new URL(route.request().url()).pathname.split('/')[3];
    sessions.find(s=>s.id===id)!.hidden=Number(route.request().postDataJSON().hidden);
    return route.fulfill({json:{ok:true}});
  });
  await page.route('**/api/sessions/*/delete',route=>{
    const id=new URL(route.request().url()).pathname.split('/')[3];
    expect(route.request().postDataJSON()).toEqual({confirmed:true});
    const index=sessions.findIndex(s=>s.id===id);
    if(index<0)return route.fulfill({status:404,json:{error:'记录不存在'}});
    sessions.splice(index,1);
    return route.fulfill({json:{ok:true}});
  });
  await page.route('**/api/projects/visibility',route=>{
    const p=route.request().postDataJSON(); sessions.filter(s=>s.project===p.project).forEach(s=>s.project_hidden=Number(p.hidden));
    return route.fulfill({json:{ok:true}});
  });
  await page.route('**/api/sync',route=>route.fulfill({json:{running:false,stage:'',done:0,total:0,imported:0,skipped:0,errors:[],warnings:[],file:''}}));
  await page.route('**/api/sources',route=>route.fulfill({json:{sources:[]}}));
  await page.route('**/api/backup/status',route=>route.fulfill({json:{directory:'/tmp/backups',job:null}}));
  await page.route('**/api/restore/status',route=>route.fulfill({json:{job:null}}));
});

test('project toggle makes search global again, recent project comes first',async({page})=>{
  await page.goto('/');
  await expect(page.locator('.project-item.active')).toHaveCount(0);
  await expect(page.locator('.project-name').first()).toHaveText('最近项目');
  await page.locator('#global-search').fill('共同关键词');
  await expect(page.locator('.conversation-card')).toHaveCount(2);
  await page.locator('[data-project="最近项目"]').click();
  await expect(page.locator('#global-search')).toHaveAttribute('aria-label','搜索项目 最近项目 的对话');
  await expect(page.locator('.conversation-card')).toHaveCount(1);
  await page.locator('[data-project="最近项目"]').click();
  await expect(page.locator('.project-item.active')).toHaveCount(0);
  await expect(page.locator('#global-search')).toHaveAttribute('aria-label','搜索所有项目的对话');
  await expect(page.locator('.conversation-card')).toHaveCount(2);
});

test('hidden conversations and projects only display with the switch and can be restored',async({page})=>{
  await page.goto('/');
  await page.locator('[data-id="recent"]').click();
  await page.getByRole('button',{name:'隐藏对话',exact:true}).click();
  await expect(page.locator('.conversation-card')).toHaveCount(1);
  await expect(page.locator('.reader-header')).toHaveCount(0);
  await page.locator('#show-hidden').click();
  await expect(page.locator('.conversation-card')).toHaveCount(3);
  await page.locator('[data-id="recent"]').click();
  await page.getByRole('button',{name:'恢复对话',exact:true}).click();
  await expect(page.getByRole('button',{name:'隐藏对话',exact:true})).toBeVisible();
  await page.locator('#show-hidden').click();
  await page.getByRole('button',{name:'隐藏项目 最近项目',exact:true}).click();
  await expect(page.locator('[data-project="最近项目"]')).toHaveCount(0);
  await expect(page.locator('.conversation-card')).toHaveCount(1);
  await page.locator('#show-hidden').click();
  await page.getByRole('button',{name:'恢复项目 最近项目',exact:true}).click();
  await page.locator('#show-hidden').click();
  await expect(page.locator('.conversation-card')).toHaveCount(2);
});

test('compressed backup progress and complete restore instructions are shown separately from ChatGPT imports',async({page})=>{
  let job:object|null=null;
  await page.route('**/api/restore/status',route=>route.fulfill({json:{job}}));
  await page.route('**/api/restore',route=>{
    expect(route.request().postDataJSON().path).toBe('D:/备份/echo.sqlite.zst');
    job={id:'restore-ui',stage:'ready',duration:4,done_bytes:100,total_bytes:100,path:'/tmp/restored.sqlite',command:"./bin/echo-vault --data '/tmp/restored.sqlite'"};
    return route.fulfill({json:{job}});
  });
  await page.goto('/');
  await page.locator('#sources-button').click();
  await expect(page.locator('#backup-format')).toHaveValue('zstd');
  await expect(page.locator('#import-file')).toHaveAttribute('accept','.json,.zip');
  await expect(page.locator('#restore-file')).toHaveAttribute('accept','.sqlite,.sqlite3,.db,.zst');
  await page.locator('#restore-path').fill('D:/备份/echo.sqlite.zst');
  await page.locator('#restore-path-button').click();
  await expect(page.locator('#restore-progress')).toContainText('完整档案已恢复');
  await expect(page.locator('#restore-progress')).toContainText("--data '/tmp/restored.sqlite'");
  await expect(page.locator('#restore-progress')).toContainText('当前数据文件仍然保留');
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBeTruthy();
});


test('deleting requires confirmation, removes the empty selected project, and clears reader and URL',async({page})=>{
  let requests=0;
  page.on('request',request=>{if(request.url().endsWith('/recent/delete'))requests++;});
  await page.goto('/');
  await page.locator('[data-project="最近项目"]').click();
  await page.locator('[data-id="recent"]').click();
  await expect(page.locator('#session-visibility + #session-delete')).toBeVisible();
  await page.getByRole('button',{name:'删除对话',exact:true}).click();
  const dialog=page.getByRole('dialog',{name:'删除对话？'});
  await expect(dialog).toContainText('最近项目的共同关键词');
  await expect(dialog).toContainText('完整重新扫描');
  expect(requests).toBe(0);
  await dialog.getByRole('button',{name:'取消',exact:true}).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.locator('[data-id="recent"]')).toBeVisible();
  await page.locator('#session-delete').click();
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  expect(requests).toBe(0);
  await page.locator('#session-delete').click();
  await dialog.getByRole('button',{name:'确认删除',exact:true}).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.locator('[data-project="最近项目"]')).toHaveCount(0);
  await expect(page.locator('[data-id="recent"]')).toHaveCount(0);
  await expect(page.locator('[data-id="old"]')).toBeVisible();
  await expect(page.locator('.reader-header')).toHaveCount(0);
  await expect(page.locator('#all-count')).toHaveText('1');
  expect(new URL(page.url()).searchParams.has('session')).toBe(false);
  expect(new URL(page.url()).searchParams.has('project')).toBe(false);
  expect(requests).toBe(1);
  await page.locator('#show-hidden').click();
  await expect(page.locator('[data-id="recent"]')).toHaveCount(0);
  await expect(page.locator('[data-project="最近项目"]')).toHaveCount(0);
});

test('a project remains until all its conversations are deleted, including archived ones',async({page})=>{
  await page.goto('/');
  await page.locator('#show-hidden').click();
  await page.locator('[data-project="旧项目"]').click();
  await page.locator('[data-id="old"]').click();
  await page.locator('#session-delete').click();
  await page.locator('#delete-session-confirm').click();
  await expect(page.locator('[data-project="旧项目"] b')).toHaveText('1');
  await expect(page.locator('[data-id="archived"]')).toBeVisible();
  await page.locator('[data-id="archived"]').click();
  await page.locator('#session-delete').click();
  await page.locator('#delete-session-confirm').click();
  await expect(page.locator('[data-project="旧项目"]')).toHaveCount(0);
  await expect(page.locator('[data-id="recent"]')).toBeVisible();
});

test('delete failures keep the conversation and show a retryable error',async({page})=>{
  await page.route('**/api/sessions/recent/delete',route=>route.fulfill({status:400,json:{error:'档案暂时不可写，请重试'}}));
  await page.goto('/');
  await page.locator('[data-id="recent"]').click();
  await page.locator('#session-delete').click();
  await page.locator('#delete-session-confirm').click();
  await expect(page.locator('#delete-session-error')).toHaveText('档案暂时不可写，请重试');
  await expect(page.locator('#delete-session-dialog')).toBeVisible();
  await expect(page.locator('#delete-session-confirm')).toBeEnabled();
  await expect(page.locator('[data-id="recent"]')).toBeVisible();
  await expect(page.locator('.reader-header')).toContainText('最近项目的共同关键词');
  await page.locator('#delete-session-cancel').click();
  await expect(page.locator('#session-delete')).toBeVisible();
});

test('full scans explain that deleted conversations can be reimported and honor cancellation',async({page})=>{
  let fullRequests=0;
  await page.route('**/api/sync',route=>{
    if(route.request().method()==='POST'){
      expect(route.request().postDataJSON()).toEqual({full:true});
      fullRequests++;
      return route.fulfill({json:{started:true}});
    }
    return route.fulfill({json:{running:false,done:0,total:0,imported:0,errors:[],warnings:[]}});
  });
  await page.goto('/');
  await page.locator('#sources-button').click();
  page.once('dialog',async dialog=>{expect(dialog.message()).toContain('重新收录已删除的对话');await dialog.dismiss();});
  await page.locator('#full-sync').click();
  expect(fullRequests).toBe(0);
  page.once('dialog',async dialog=>{await dialog.accept();});
  await page.locator('#full-sync').click();
  await expect.poll(()=>fullRequests).toBe(1);
});

test('mobile deletion controls fit and pending deletion cannot be submitted twice or dismissed',async({page})=>{
  let complete!:()=>void, requests=0;
  const pending=new Promise<void>(resolve=>{complete=resolve;});
  await page.route('**/api/sessions/recent/delete',async route=>{
    requests++;
    await pending;
    return route.fulfill({status:400,json:{error:'请重试'}});
  });
  await page.setViewportSize({width:390,height:844});
  await page.goto('/');
  await page.locator('[data-id="recent"]').click();
  await expect(page.locator('#session-delete')).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  await page.locator('#session-delete').click();
  const dialog=page.locator('#delete-session-dialog');
  await expect(dialog).toBeVisible();
  expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);
  await page.screenshot({path:'artifacts/mobile-delete-confirmation.png'});
  await page.locator('#delete-session-confirm').click();
  await expect(page.locator('#delete-session-confirm')).toBeDisabled();
  await expect(page.locator('#delete-session-cancel')).toBeDisabled();
  await expect(page.locator('#delete-session-dialog [data-close-dialog]')).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeVisible();
  await expect.poll(()=>requests).toBe(1);
  complete();
  await expect(page.locator('#delete-session-error')).toHaveText('请重试');
  await expect(page.locator('#delete-session-cancel')).toBeEnabled();
  await page.locator('#delete-session-cancel').click();
  await expect(dialog).not.toBeVisible();
  await expect(page.locator('.reader-header')).toBeVisible();
});
