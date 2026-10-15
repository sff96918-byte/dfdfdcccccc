// Run: npm install --no-save --package-lock=false jsdom@26
//      node --test tests/browser-ui.test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');
const assets = path.join(__dirname, '../cmd/chitchatui/frontend/dist');
function setup(api = {}) {
  const html = fs.readFileSync(path.join(assets, 'index.html'), 'utf8');
  const dom = new JSDOM(html, { runScripts: 'outside-only', url: 'http://localhost/' });
  const w = dom.window;
  // Test the actual script without starting its unrelated bootstrap/timer.
  w.addEventListener = () => {};
  w.go = { main: { App: api } };
  w.eval(fs.readFileSync(path.join(assets, 'app.js'), 'utf8'));
  const options = [{key:'rod',label:'Rod',description:'Rod capture'}, {key:'chromedp',label:'ChromeDP',description:'CDP capture'}, {key:'show_mode',label:'Show Mode',description:'Visible Rod capture'}];
  w.renderBrowserOptions(options);
  return { dom, w, d:w.document, options };
}
const plain = x => JSON.parse(JSON.stringify(x));
test('numbered list, count, per-row Run and no old save/radio/central button', () => {
  const {dom,d} = setup();
  assert.equal(d.querySelector('#browser-available').textContent, 'Available: 3');
  assert.deepEqual([...d.querySelectorAll('.browser-number')].map(x=>x.textContent), ['01','02','03']);
  assert.equal(d.querySelectorAll('.browser-run').length, 3);
  assert.equal(d.querySelectorAll('[name="browser-backend"], #run-fresh-browsers, #browser-form [type="submit"]').length, 0);
  assert.equal(d.querySelector('#browser-status').textContent, 'Status: Ready');
  dom.window.close();
});
test('clicked ChromeDP sends live inputs, prevents overlapping clicks, and leaves form edits intact', async () => {
  let resolve; const calls = [];
  const {dom,w,d,options} = setup({RunBrowser:(...args)=>{calls.push(plain(args));return new Promise(r=>resolve=r);}});
  d.querySelector('#browser-profile').value = ' new-profile ';
  d.querySelector('#browser-headless').checked = true;
  d.querySelector('#fresh-browser-count').value = '3';
  const pending = w.runBrowserOption(options[1]);
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>b.disabled));
  await w.runBrowserOption(options[0]);
  assert.deepEqual(calls, [[{backend:'chromedp',profile:'new-profile',headless:true},3]]);
  d.querySelector('#browser-profile').value = 'edited-while-pending';
  w.renderBrowserOptions(options); // refreshing must preserve pending button state
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>b.disabled));
  resolve(); await pending;
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>!b.disabled));
  assert.equal(d.querySelector('#browser-profile').value, 'edited-while-pending');
  assert.match(d.querySelector('#browser-status').textContent,/request accepted/);
  dom.window.close();
});
test('each row button dispatches its own backend; blank profile defaults', async () => {
  const calls=[]; const {dom,d} = setup({RunBrowser:async (...a)=>calls.push(plain(a))});
  for(const button of d.querySelectorAll('.browser-run')) {
    button.click(); await new Promise(r=>setImmediate(r));
  }
  assert.deepEqual(calls.map(a=>a[0].backend),['rod','chromedp','show_mode']);
  assert.equal(calls[0][0].profile,'default');
  dom.window.close();
});
test('invalid count never invokes backend', async () => {
  let calls=0; const {dom,w,d,options}=setup({RunBrowser:async()=>calls++});
  for(const value of ['', '0','6','1.5','-1']) {
    d.querySelector('#fresh-browser-count').value=value;
    await w.runBrowserOption(options[0]);
    assert.match(d.querySelector('#browser-status').textContent,/between 1 and 5/);
  }
  assert.equal(calls,0); dom.window.close();
});
test('backend failure and unavailable binding show errors and restore buttons', async () => {
  const {dom,w,d,options}=setup({RunBrowser:async()=>{throw new Error('Proxy is invalid');}});
  await w.runBrowserOption(options[0]);
  assert.match(d.querySelector('#browser-status').textContent,/Proxy is invalid/);
  assert.ok(d.querySelector('#browser-status').classList.contains('browser-error'));
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>!b.disabled));
  delete w.go.main.App.RunBrowser;
  await w.runBrowserOption(options[0]);
  assert.match(d.querySelector('#browser-status').textContent,/Wails binding not ready/);
  dom.window.close();
});
test('fresh-browser worker status wins over late request acknowledgement', async () => {
  let resolve; const events={}; const {dom,w,d,options}=setup({RunBrowser:()=>new Promise(r=>resolve=r)});
  w.runtime={EventsOn:(name,fn)=>events[name]=fn};
  w.bindEvents();
  const pending=w.runBrowserOption(options[1]);
  events['ui:log']({level:'error',message:'[default fresh 1/1] Browser start failed: test',created_at:''});
  resolve(); await pending;
  assert.match(d.querySelector('#browser-status').textContent,/Browser start failed/);
  events['ui:log']({level:'info',message:'Unrelated chat activity',created_at:''});
  assert.match(d.querySelector('#browser-status').textContent,/Browser start failed/);
  dom.window.close();
});
test('state refresh initializes browser defaults only once', () => {
  const {dom,w,d,options}=setup();
  const snapshot={browser:{profile:'saved',headless:false},browser_options:options,message:{},proxy:{},general:{},home:{}};
  w.fillForms(snapshot);
  d.querySelector('#browser-profile').value='draft';
  d.querySelector('#browser-headless').checked=true;
  d.querySelector('#fresh-browser-count').value='5';
  w.fillForms(snapshot);
  assert.equal(d.querySelector('#browser-profile').value,'draft');
  assert.equal(d.querySelector('#browser-headless').checked,true);
  assert.equal(d.querySelector('#fresh-browser-count').value,'5');
  dom.window.close();
});
test('browser form submit is prevented and does not choose a backend', () => {
  let calls=0; const {dom,w,d}=setup({RunBrowser:()=>calls++});
  w.bindForms();
  const event=new w.Event('submit',{cancelable:true});
  d.querySelector('#browser-form').dispatchEvent(event);
  assert.ok(event.defaultPrevented); assert.equal(calls,0);
  dom.window.close();
});
test('backend labels and descriptions are escaped', () => {
  const {dom,w,d}=setup();
  w.renderBrowserOptions([{key:'rod',label:'<img src=x>',description:'<script>bad()</script>'}]);
  assert.equal(d.querySelector('#browser-options img'),null);
  assert.equal(d.querySelector('#browser-options script'),null);
  assert.equal(d.querySelector('#browser-available').textContent,'Available: 1');
  dom.window.close();
});

test('routine cleanup does not hide the login result or launch failure', () => {
  const events={}; const {dom,w,d}=setup();
  w.runtime={EventsOn:(name,fn)=>events[name]=fn}; w.bindEvents();
  for(const message of ['Browser start failed: test','Login saved to test.json']) {
    events['ui:log']({level:message.startsWith('Browser')?'error':'info',message:`[default fresh 1/1] ${message}`,created_at:''});
    events['ui:log']({level:'info',message:'[default fresh 1/1] Removed temporary browser data from temporary-test-path',created_at:''});
    assert.ok(d.querySelector('#browser-status').textContent.includes(message));
  }
  dom.window.close();
});

test('Show Mode always requests a visible browser without changing the Headless preference', async () => {
  const calls=[]; const {dom,w,d,options}=setup({RunBrowser:async(...args)=>calls.push(plain(args))});
  d.querySelector('#browser-headless').checked=true;
  d.querySelector('#browser-profile').value='visible-test';
  d.querySelector('#fresh-browser-count').value='2';
  await w.runBrowserOption(options[2]);
  assert.deepEqual(calls[0],[{backend:'show_mode',profile:'visible-test',headless:false},2]);
  assert.equal(d.querySelector('#browser-headless').checked,true);
  await w.runBrowserOption(options[0]);
  assert.equal(calls[1][0].headless,true);
  dom.window.close();
});

test('Home has no log stream; snapshots/events cannot recreate it or break browser status', async () => {
  const events={};
  const {dom,w,d,options}=setup({GetInitialState:async()=>({browser:{profile:'saved',headless:false,backend:'rod'},browser_options:options,message:{sequence:'a\nb'},proxy:{},general:{},home:{},logs:[{level:'activity',message:'old private activity'}]})});
  try {
    w.runtime={EventsOn:(name,fn)=>events[name]=fn};w.bindEvents();
    await w.refreshState();
    assert.equal(d.querySelector('#log-list, .log-entry'),null);
    assert.doesNotMatch(d.querySelector('[data-panel="home"]').textContent,/old private activity|Showing bot logs/);
    assert.match(d.querySelector('[data-panel="home"]').textContent,/logs\.txt/);
    for(const level of ['activity','error','info']) events['ui:log']({level,message:'[default fresh 1/1] Browser diagnostic'});
    assert.equal(d.querySelector('#log-list, .log-entry'),null);
    assert.match(d.querySelector('#browser-status').textContent,/Browser diagnostic/);
    for(const id of ['clean-profiles','refresh-state','clear-logs','stop-bot','start-bot','stat-bot-accounts','stat-running-accounts','stat-messages-sent']) assert.ok(d.getElementById(id));
  } finally {dom.window.close();}
});

test('backend failures are forwarded to file logger via Wails without settings arguments', async () => {
  const {dom,w}=setup({RunBrowser:async()=>{throw new Error('synthetic backend failure');}});
  const messages=[];w.runtime={LogError:message=>messages.push(message)};
  try {
    await assert.rejects(w.invoke('RunBrowser',{password:'DO_NOT_LOG_ARGUMENTS'}),/synthetic backend failure/);
    assert.equal(messages.length,1);assert.match(messages[0],/Backend RunBrowser failed.*synthetic backend failure/s);
    assert.doesNotMatch(messages[0],/DO_NOT_LOG_ARGUMENTS/);
    w.runtime.LogError=()=>{throw new Error('logging bridge unavailable');};
    await assert.rejects(w.invoke('RunBrowser',{}),/synthetic backend failure/);
  } finally {dom.window.close();}
});

test('Clear Logs still clears only legacy UI history and preserves Home controls', async () => {
  let cleared=0;
  const {dom,w,d,options}=setup({ClearLogs:async()=>cleared++,GetInitialState:async()=>({browser:{},browser_options:options,message:{},proxy:{},general:{},home:{},logs:[]})});
  try {
    w.bindActions();d.getElementById('clear-logs').click();
    await new Promise(r=>setImmediate(r));assert.equal(cleared,1);
    assert.match(d.getElementById('clear-logs').title,/logs\.txt is kept/);
    assert.equal(d.querySelector('#log-list'),null);
  } finally {dom.window.close();}
});

test('uncaught frontend errors and rejected promises are forwarded without rendering Home logs', () => {
  const {dom,w,d}=setup();const listeners={},messages=[];
  try {
    w.addEventListener=(name,fn)=>listeners[name]=fn;
    w.eval(fs.readFileSync(path.join(assets,'app.js'),'utf8').replace('const state = {','var state = {'));
    w.runtime={LogError:message=>messages.push(message)};
    listeners.error({error:new Error('synthetic JS failure')});
    listeners.unhandledrejection({reason:'synthetic rejected promise'});
    assert.equal(messages.length,2);assert.match(messages[0],/Uncaught error.*synthetic JS failure/s);
    assert.match(messages[1],/Unhandled promise rejection.*synthetic rejected promise/s);
    assert.equal(d.querySelector('#log-list, .log-entry'),null);
  } finally {dom.window.close();}
});

test('Home transcript renders full actual SMS in DOM order, never technical activity or HTML',()=>{
 const {dom,w,d}=setup();
 try {
  const long='বাংলা\n🙂 '+ 'x'.repeat(20000);
  w.renderChatTranscript({version:1,selected:'one',sessions:[{id:'one',label:'browser',active:true}],messages:[
   {id:'u2',role:'user',text:'same',order:3},{id:'u1',role:'user',text:'same',order:1},
   {id:'b1',role:'bot',text:long,order:2},{id:'b2',role:'bot',text:'<img src=x onerror=alert(1)>',order:4},
   {id:'u1',role:'user',text:'duplicate',order:1},{id:'bad',role:'unknown',text:'not guessed',order:5}
  ]});
  const rows=[...d.querySelectorAll('.home-chat-row')];
  assert.deepEqual(rows.map(r=>r.querySelector('strong').textContent),['User:','Bot:','User:','Bot:']);
  assert.equal(rows[1].querySelector('span').textContent,long);
  assert.equal(d.querySelector('#chat-messages img'),null);
  assert.equal(d.querySelector('#log-list, .log-entry'),null);
  assert.match(d.querySelector('#chat-messages').textContent,/<img src=x/);
 } finally {dom.window.close();}
});

test('transcript UI coalesces events and ignores stale responses after conversation selection',async()=>{
 let resolve;const calls=[];const events={};
 const {dom,w,d}=setup({GetLiveChatSnapshot:key=>{calls.push(key);return new Promise(r=>resolve=r);}});
 try {
  w.runtime={EventsOn:(name,fn)=>events[name]=fn};w.bindEvents();
  const first=w.refreshChatTranscript();events['ui:chat_changed']({version:2});events['ui:chat_changed']({version:3});
  assert.equal(calls.length,1);
  const option=d.createElement('option');option.value='selected';option.textContent='selected';d.getElementById('chat-session').append(option);
  d.getElementById('chat-session').value='selected';d.getElementById('chat-session').dispatchEvent(new w.Event('change'));
  resolve({version:1,selected:'old',sessions:[{id:'old',label:'old',active:false}],messages:[{id:'old',role:'bot',text:'stale reply',order:1}]});
  await first;await new Promise(r=>setImmediate(r));assert.deepEqual(calls,['','selected']);
  assert.doesNotMatch(d.getElementById('chat-messages').textContent,/stale reply/);
  resolve({version:3,selected:'selected',sessions:[{id:'selected',label:'selected',active:true}],messages:[{id:'ok',role:'user',text:'chosen conversation',order:1}]});
  await new Promise(r=>setImmediate(r));assert.match(d.getElementById('chat-messages').textContent,/chosen conversation/);
  events['ui:log']({level:'activity',message:'SEND CONFIRMED technical only'});
  assert.doesNotMatch(d.getElementById('chat-messages').textContent,/SEND CONFIRMED/);
 } finally {dom.window.close();}
});

test('transcript keeps reader position, follows bottom, and announces bounded history',()=>{
 const {dom,w,d}=setup();
 try {
  const box=d.getElementById('chat-messages');Object.defineProperty(box,'scrollHeight',{value:1000,configurable:true});Object.defineProperty(box,'clientHeight',{value:200,configurable:true});
  const snapshot={version:1,selected:'one',sessions:[{id:'one',label:'one',active:false,dropped:30}],evicted:2,messages:[{id:'a',role:'user',text:'last',order:1}]};
  w.renderChatTranscript(snapshot);box.scrollTop=100;w.renderChatTranscript(snapshot);assert.equal(box.scrollTop,100);
  box.scrollTop=800;w.renderChatTranscript(snapshot);assert.equal(box.scrollTop,1000);
  assert.match(d.getElementById('chat-history-note').textContent,/30 older messages/);
  assert.match(d.getElementById('chat-history-note').textContent,/2 older conversations/);
 } finally {dom.window.close();}
});

test('Test Proxy checks unsaved form without saving, blocks repeats and renders literal results', async () => {
  let resolve; const calls=[];
  const {dom,w,d}=setup({TestProxyEntries:raw=>{calls.push(raw);return new Promise(r=>resolve=r);},SaveProxySettings:()=>{throw Error('must not save');}});
  d.querySelector('#proxy-enabled').checked=false;
  d.querySelector('#proxy-entries').value='socks5://host:1080:u:p\nhttp://host2:80';
  const pending=w.testProxyEntries();
  assert.equal(d.querySelector('#test-proxy').disabled,true);
  await w.testProxyEntries();assert.equal(calls.length,1);
  assert.equal(calls[0],d.querySelector('#proxy-entries').value);
  d.querySelector('#proxy-entries').value='edited:80';
  resolve([{line:1,label:'socks5://host:1080',ok:true,status:200,latency_ms:12,detail:'PASS'}, {line:2,label:'<img src=x>',ok:false,tunnel_ok:true,status:403,latency_ms:9,detail:'<script>bad</script>',provider_message:'quota <b>exhausted</b>',proxy_authenticate:'Basic realm="proxy"',hint:'Test another proxy plan.'}, {line:3,label:'http://p.example:80',ok:false,status:402,latency_ms:250,detail:'HTTP proxy rejected CONNECT (402)',authenticated:false,hint:'The proxy answered 402 for a line without credentials.'}]);
  await pending;
  assert.equal(d.querySelector('#test-proxy').disabled,false);
  assert.equal(d.querySelector('#test-proxy').textContent,'Test Proxy');
  assert.match(d.querySelector('#proxy-test-status').textContent,/1\/3 passed.*List edited/);
  assert.equal(d.querySelector('#proxy-test-results img, #proxy-test-results script'),null);
  assert.match(d.querySelector('#proxy-test-results').textContent,/REACHABLE \/ SITE RESTRICTED.*HTTP 403/);
  assert.match(d.querySelector('#proxy-test-results').textContent,/Provider said: “quota <b>exhausted<\/b>”/);
  assert.match(d.querySelector('#proxy-test-results').textContent,/Auth challenge: Basic realm="proxy"/);
  assert.match(d.querySelector('#proxy-test-results').textContent,/Next step: Test another proxy plan\./);
  assert.match(d.querySelector('#proxy-test-results').textContent,/FAIL — http:\/\/p\.example:80 — 250 ms — HTTP 402/);
  assert.match(d.querySelector('#proxy-test-results').textContent,/Next step: The proxy answered 402 for a line without credentials\./);
  assert.equal(d.querySelector('#proxy-entries').value,'edited:80');
  dom.window.close();
});
test('Test Proxy empty/error handling and SOCKS5 default do not alter explicit HTTP lines', async () => {
  const calls=[];const {dom,w,d}=setup({TestProxyEntries:raw=>{calls.push(raw);throw Error('synthetic test error');}});
  await w.testProxyEntries();assert.equal(calls.length,0);
  d.querySelector('#proxy-default-scheme').value='socks5';
  d.querySelector('#proxy-entries').value='host:1080:u:p\nhttp://other:80\n\n';
  assert.equal(w.proxyInputLines(),'socks5://host:1080:u:p\nhttp://other:80\n\n');
  await w.testProxyEntries();assert.equal(calls.length,1);
  assert.match(d.querySelector('#proxy-test-status').textContent,/synthetic test error/);
  assert.equal(d.querySelector('#test-proxy').disabled,false);
  dom.window.close();
});
test('Proxy button is bound inside the existing form and does not submit Save Proxy', () => {
  const {dom,w,d}=setup();
  w.bindForms();
  assert.equal(d.querySelector('#test-proxy').type,'button');
  dom.window.close();
});
