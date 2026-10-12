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
