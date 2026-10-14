import {test,expect} from '@playwright/test';
import {readFile} from 'node:fs/promises';
import {resolve} from 'node:path';

for (const outcome of ['success','failure','partial'] as const) {
  test(`single conversation sync ${outcome} refreshes the reader and reports its actual result`,async({page})=>{
    const stamp=Date.now()/1000;
    const id='chatgpt:chosen';
    let running=false,started=false,done=false,reads=0;
    const calls:string[]=[];
    const session={id,title:'目标会话',project:'ChatGPT',provider:'ChatGPT',platform:'Windows',metadata:{desktop_cache:true},created:stamp,updated:stamp,message_count:1,total_count:1,favorite:0,hidden:0,project_hidden:0};
    await page.route('**/*',async route=>{
      const url=new URL(route.request().url());const path=decodeURIComponent(url.pathname);
      if(path==='/api/sessions/'+id+'/sync') {calls.push(path);started=true;running=true;return route.fulfill({json:{started:true}});}
      if(path==='/api/sync') {
        if(route.request().method()==='POST')throw Error('Targeted sync must not start a global scan');
        return route.fulfill({json:{running,automatic:false,stage:running?'chatgpt_desktop_live':'done',done:done?1:0,total:1,imported:done?1:0,skipped:0,errors:done&&outcome==='failure'?[{path:'Desktop',error:'桌面增强入口未就绪，请通过启动器打开 Desktop'}]:[],warnings:[],file:'目标会话',started:stamp,finished:done?stamp+1:stamp,duration:1,...(started?{session_id:id}:{}),...(done&&outcome!=='failure'?{session_result:{complete:outcome!=='partial',pages:2,messages:2,streaming:false,missing_bodies:0}}:{})}});
      }
      if(path==='/api/sessions/'+id) {
        reads++;
        const body=done&&outcome!=='failure'?'重新读取后的完整内容':'原先的部分内容';
        return route.fulfill({json:{session:{...session,message_count:done?2:1},items:[{id:7,role:'assistant',body,timestamp:stamp,metadata:{},length:body.length}],offset:0,total:done?2:1,more:false}});
      }
      if(path==='/api/sessions')return route.fulfill({json:{items:[session],more:false,offset:0}});
      if(path==='/api/stats')return route.fulfill({json:{sessions:1,messages:1,events:1,projects:1,favorites:0,projects_list:[],platforms:[],providers:[],last_sync:stamp,db_bytes:0,raw_bytes:0}});
      if(path==='/api/backup/status')return route.fulfill({json:{directory:'',job:null}});
      if(path==='/api/restore/status')return route.fulfill({json:{job:null}});
      if(path.startsWith('/api/'))throw Error('Unexpected API '+path);
      const asset=url.pathname.startsWith('/assets/')?url.pathname.slice(1):'index.html';
      return route.fulfill({body:await readFile(resolve('dist',asset)),contentType:asset.endsWith('.js')?'application/javascript':asset.endsWith('.css')?'text/css':'text/html'});
    });
    await page.goto('/?session='+encodeURIComponent(id));
    await expect(page.locator('#messages')).toContainText('原先的部分内容');
    const button=page.getByRole('button',{name:'重新同步当前对话'});
    await button.click();
    await expect(button).toBeDisabled();
    await expect(page.locator('#session-sync-feedback')).toContainText('正在重新读取');
    expect(calls).toEqual(['/api/sessions/'+id+'/sync']);
    running=false;done=true;
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await expect(button).toBeEnabled();
    if(outcome==='failure') {
      await expect(page.locator('#session-sync-feedback')).toContainText('桌面增强入口未就绪');
      await expect(page.locator('#session-sync-feedback')).not.toContainText('已保存');
    } else {
      await expect(page.locator('#messages')).toContainText('重新读取后的完整内容');
      await expect(page.locator('#session-sync-feedback')).toContainText(outcome==='partial'?'点击重新同步继续读取':'接口分页已读完');
    }
    expect(reads).toBeGreaterThan(1);
  });
}
