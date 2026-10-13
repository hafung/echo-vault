import {test,expect} from '@playwright/test';
import {readFile} from 'node:fs/promises';
import {resolve} from 'node:path';

test('open page starts incremental sync after five minutes, refreshes, and keeps automatic completion quiet',async({page})=>{
  const initial = new Date('2026-10-13T08:00:00Z');
  await page.clock.install({time:initial});
  let report={running:false,automatic:false,stage:'done',done:0,total:0,imported:0,skipped:0,errors:[],warnings:[],file:'',started:initial.getTime()/1000-10,finished:initial.getTime()/1000,duration:10};
  const requests:unknown[]=[];
  let statsReads=0;
  await page.route('**/*',async route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/sync'){
      if(route.request().method()==='POST'){
        requests.push(route.request().postDataJSON());
        const current=await page.evaluate(()=>Date.now()/1000);
        report={...report,automatic:true,started:current,finished:current,imported:1};
        return route.fulfill({json:{started:true}});
      }
      return route.fulfill({json:report});
    }
    if(url.pathname==='/api/stats'){statsReads++;return route.fulfill({json:{sessions:0,messages:0,events:0,projects:0,favorites:0,projects_list:[],platforms:[],providers:[],last_sync:report.finished,db_bytes:0,raw_bytes:0}});}
    if(url.pathname==='/api/sessions')return route.fulfill({json:{items:[],more:false,offset:0}});
    if(url.pathname==='/api/restore/status')return route.fulfill({json:{job:null}});
    if(url.pathname==='/api/backup/status')return route.fulfill({json:{directory:'',job:null}});
    if(url.pathname.startsWith('/api/'))throw new Error('Unexpected API '+url.pathname);
    const asset=url.pathname.startsWith('/assets/')?url.pathname.slice(1):'index.html';
    return route.fulfill({body:await readFile(resolve('dist',asset)),contentType:asset.endsWith('.js')?'application/javascript':asset.endsWith('.css')?'text/css':'text/html'});
  });
  await page.goto('/');
  await expect(page.locator('#sync-status')).toContainText('档案已更新');
  await page.clock.fastForward(4*60*1000+59000);
  await expect(page.locator('#sync-status')).toContainText('档案已更新');
  expect(requests).toHaveLength(0);
  const previousReads=statsReads;
  await page.clock.fastForward(10000);
  await expect.poll(()=>requests.length).toBe(1);
  expect(requests[0]).toEqual({full:false,automatic:true});
  await expect.poll(()=>statsReads).toBeGreaterThan(previousReads);
  await expect(page.locator('#toast')).not.toHaveClass(/visible/);
  await page.clock.fastForward(4*60*1000);
  expect(requests).toHaveLength(1);
  // Returning from a suspended tab immediately checks for an overdue run.
  await page.clock.fastForward(2*60*1000);
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
  await expect.poll(()=>requests.length).toBe(2);
});
