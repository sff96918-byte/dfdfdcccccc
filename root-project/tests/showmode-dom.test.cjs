const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {JSDOM} = require('jsdom');
const helper = fs.readFileSync(path.join(__dirname, '../internal/browser/showmode_dom.js'), 'utf8');
const html = fs.readFileSync(path.join(__dirname, 'fixtures/showmode-chat.html'), 'utf8');
function setup(t, markup = html) {
 const dom = new JSDOM(markup, {runScripts:'outside-only', url:'https://app.chitchat.gg/text'});
 t.after(async()=>{ api.dispose(); await flush(); dom.window.close(); });
 // jsdom has no layout: emulate geometry ONLY, not selectors or parser logic.
 dom.window.HTMLElement.prototype.getBoundingClientRect = ()=>({width:100,height:20});
 const api = dom.window.eval(helper+'\n__sm;');
 return {w:dom.window,d:dom.window.document,api};
}
const flush = async()=>{ await Promise.resolve(); await Promise.resolve(); };
function append(d, id, author, text) {
 const li=d.createElement('li');li.dataset.messageId=id;
 if(author){const h=d.createElement('h3'),b=d.createElement('button');b.textContent=author;h.append(b);li.append(h);}
 const p=d.createElement('p');p.dataset.from=id;p.textContent=text;li.append(p);
 d.querySelector('ol').append(li);return li;
}
const plain=x=>JSON.parse(JSON.stringify(x));
test('captured layout: 3 IDs, grouped author and invisible time are parsed without guessing', t=>{
 const {api}=setup(t);const s=api.snapshot();
 assert.deepEqual(plain(s.messages).map(m=>[m.id,m.author,m.text]),[
  ['row-1','Test Partner','hello'],['row-2','Test Partner','hello again'],['row-3','Test Self','greeting']]);
 assert.equal(s.controls.partner,'Test Partner');assert.equal(s.controls.ownName,'Test Self');
 assert.equal(s.controls.canSend,true);assert.equal(s.controls.observerReady,true);
});
test('data-from is never interpreted as author identity',t=>{const {api}=setup(t);assert.equal(api.snapshot().messages[1].author,'Test Partner');});
test('only the exact composer input is chosen, not hidden sizing/search textareas',t=>{
 const {api,d}=setup(t);assert.equal(api.input().getAttribute('aria-label'),'Send a message');
 d.querySelector('[data-chat-composer]').remove();d.body.innerHTML+='<input placeholder="message">';
 assert.equal(api.input(),null);assert.equal(api.snapshot().controls.canSend,false);
});
test('active aria label excludes hidden START/STOP and substring matching',t=>{
 const {api,d}=setup(t);let clicks=0;const b=d.querySelector('[aria-keyshortcuts]');b.onclick=()=>clicks++;
 assert.deepEqual(plain(api.snapshot().controls.buttons),['SKIP']);assert.equal(api.click(['START']),'');
 assert.equal(api.click(['SKI']),'');assert.equal(clicks,0);assert.equal(api.click(['SKIP']),'SKIP');assert.equal(clicks,1);
 b.setAttribute('aria-label','CONFIRM?');assert.equal(api.click(['SKIP']),'');assert.equal(api.click(['CONFIRM?']),'CONFIRM?');
});
test('disabled/busy cycle control cannot be clicked or used to send',t=>{
 const {api,d}=setup(t);const b=d.querySelector('[aria-keyshortcuts]');b.setAttribute('aria-busy','true');
 assert.equal(api.snapshot().controls.canSend,false);assert.equal(api.click(['SKIP']),'');
 b.setAttribute('aria-busy','false');b.disabled=true;assert.equal(api.click(['SKIP']),'');
});
test('same text with different IDs remains distinct, rerender with same ID stays one row',t=>{
 const {api,d}=setup(t);api.snapshot();append(d,'row-4','Test Partner','hello');
 assert.equal(api.snapshot().messages.length,4);d.querySelector('[data-message-id="row-4"]').replaceWith(d.querySelector('[data-message-id="row-4"]').cloneNode(true));
 assert.equal(api.snapshot().messages.length,4);
});
test('observer retains new row even when it leaves DOM before the next Go poll',async t=>{
 const {api,d}=setup(t);api.snapshot();const li=append(d,'brief','Test Partner','brief text');await flush();li.remove();await flush();
 assert.equal(api.snapshot().messages.find(m=>m.id==='brief').text,'brief text');
});
test('text node hydration and late author resolution are observed',async t=>{
 const {api,d}=setup(t);api.snapshot();const li=append(d,'hydrate','Test Partner','');await flush();
 assert.equal(api.snapshot().messages.some(m=>m.id==='hydrate'),false);
 li.querySelector('p').textContent='hydrated';await flush();assert.equal(api.snapshot().messages.find(m=>m.id==='hydrate').text,'hydrated');
});
test('unknown leading group is retained as unknown, not guessed from message text',t=>{
 const {api,d}=setup(t);d.querySelector('ol').replaceChildren();append(d,'unknown','','Test Partner');
 assert.equal(api.snapshot().messages[0].author,'');
});
test('container replacement preserves same conversation and IDs; watchdog resumes observer',async t=>{
 const {api,d}=setup(t);const before=api.snapshot();const v=d.querySelector('[data-message-viewport]');v.replaceWith(v.cloneNode(true));await flush();
 assert.equal(api.snapshot().controls.conversation,before.controls.conversation);
 append(d,'after-remount','Test Partner','later');await flush();assert.equal(api.snapshot().messages.length,4);
});
test('partner switch clears old queue and changes conversation identity',t=>{
 const {api,d}=setup(t);const before=api.snapshot();d.querySelector('[data-conversation-header] button').setAttribute('aria-label','View @Other Partner profile');
 d.querySelector('ol').replaceChildren();append(d,'other','Other Partner','new');const after=api.snapshot();
 assert.notEqual(after.controls.conversation,before.controls.conversation);assert.equal(after.messages.length,1);
});
test('same-name rematch with replaced intro and disjoint IDs gets a fresh generation',t=>{
 const {api,d}=setup(t);const before=api.snapshot();const intro=d.querySelector('[data-testid="match-found"]').cloneNode(true);
 d.querySelector('ol').replaceChildren(intro);append(d,'next-match','Test Partner','new chat');
 assert.notEqual(api.snapshot().controls.conversation,before.controls.conversation);
});
test('START/STOP states are lobby/matching, not a live conversation',t=>{
 const {api,d}=setup(t);api.snapshot();const b=d.querySelector('[aria-keyshortcuts]');b.setAttribute('aria-label','START');
 assert.equal(api.snapshot().controls.phase,'lobby');b.setAttribute('aria-label','STOP');assert.equal(api.snapshot().controls.phase,'matching');
});
test('ordinary user text mentioning chat ended is not a lifecycle signal',t=>{
 const {api,d}=setup(t);append(d,'phrase','Test Partner','chat ended yesterday');assert.equal(api.snapshot().controls.phase,'chatting');
});
test('page helper is reused and probe never contains messages/account names/query strings',t=>{
 const {api,w}=setup(t);assert.equal(w.eval(helper+'\n__sm;'),api);const p=api.probe();
 assert.match(p,/observer=true/);assert.doesNotMatch(p,/Test Partner|Test Self|hello|https?:/);
});
test('queue bound fails closed instead of silently discarding unread messages',t=>{
 const {api,d}=setup(t);d.querySelector('ol').replaceChildren();for(let i=0;i<2001;i++)append(d,'bounded-'+i,'Test Partner','x');
 const s=api.snapshot();assert.equal(s.messages.length,2000);assert.equal(s.controls.overflow,true);
});
test('Show Mode production helper/send path never uses chat network or synthetic key dispatch',()=>{
 assert.doesNotMatch(helper,/\bfetch\s*\(|new\s+WebSocket|localStorage|sessionStorage|document\.cookie|KeyboardEvent/);
 const go=fs.readFileSync(path.join(__dirname,'../internal/browser/domchat.go'),'utf8');
 assert.match(go,/el\.Input\(/);assert.match(go,/el\.Type\(input\.Enter\)/);
 assert.doesNotMatch(go,/\.Fetch\(|\.submit\(|new KeyboardEvent/);
});

test('same-name empty rematch resets before its first new incoming message',t=>{
 const {api,d}=setup(t);const before=api.snapshot();const intro=d.querySelector('[data-testid="match-found"]').cloneNode(true);
 d.querySelector('ol').replaceChildren(intro);const empty=api.snapshot();
 assert.notEqual(empty.controls.conversation,before.controls.conversation);assert.equal(empty.messages.length,0);
 append(d,'late-first','Test Partner','fresh');assert.equal(api.snapshot().controls.conversation,empty.controls.conversation);
});

test('transcript order is reserved before delayed text hydration',t=>{
 const {api,d}=setup(t);
 append(d,'late-text','Test Partner','');append(d,'next-own','Test Self','bot later');
 api.snapshot();d.querySelector('[data-message-id="late-text"] p').textContent='user first';
 const rows=api.snapshot().messages;
 assert.ok(rows.find(r=>r.id==='late-text').order<rows.find(r=>r.id==='next-own').order);
});
