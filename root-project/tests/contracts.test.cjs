const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {JSDOM}=require('jsdom');
const root=path.join(__dirname,'..');
const read=p=>fs.readFileSync(path.join(root,p),'utf8');
const ui=read('cmd/chitchatui/frontend/dist/app.js');
const go=read('app.go');
test('every literal UI backend invocation has a public Go method',()=>{
 const invoked=[...ui.matchAll(/invoke\("(\w+)"/g)].map(m=>m[1]);
 assert.ok(invoked.includes('RunBrowser'));
 for(const method of invoked) assert.match(go,new RegExp(`func \\(a \\*App\\) ${method}\\(`),method);
});
test('direct-run contract agrees across UI, Go, JS and TypeScript bindings',()=>{
 assert.match(ui,/invoke\("RunBrowser", settings, count\)/);
 assert.match(go,/func \(a \*App\) RunBrowser\(settings model\.BrowserSettings, count int\) error/);
 assert.match(read('cmd/chitchatui/frontend/wailsjs/go/main/App.js'),/RunBrowser\(arg1, arg2\)[\s\S]*?\['RunBrowser'\]\(arg1, arg2\)/);
 assert.match(read('cmd/chitchatui/frontend/wailsjs/go/main/App.d.ts'),/RunBrowser\(arg1:model.BrowserSettings,\s*arg2:number\):Promise<void>/);
});
test('all literal DOM IDs used by the frontend exist, and HTML IDs are unique',()=>{
 const dom=new JSDOM(read('cmd/chitchatui/frontend/dist/index.html'));
 const ids=[...dom.window.document.querySelectorAll('[id]')].map(n=>n.id);
 assert.equal(new Set(ids).size,ids.length,'duplicate HTML IDs');
 for(const m of ui.matchAll(/getElementById\(["']([^"']+)["']\)/g)) assert.ok(ids.includes(m[1]),`missing #${m[1]}`);
 dom.window.close();
});
test('advertised drivers match factory and Show Mode resolves to visible Rod',()=>{
 const models=read('internal/model/ui.go').split('func DefaultBrowserOptions()')[1].split('func DefaultBrowserSettings()')[0];
 const offered=[...models.matchAll(/Key:\s*"([^"]+)"/g)].map(m=>m[1]).sort();
 const available=[...read('internal/browser/backend.go').matchAll(/case "([^"]+)":/g)].map(m=>m[1]).sort();
 assert.deepEqual(offered.filter(key=>key!=='show_mode'),available);
 assert.ok(offered.includes('show_mode'));
 assert.match(go,/if settings\.Backend == "show_mode" \{\s*settings\.Backend = "rod"\s*settings\.Headless = false/);
});
test('live browser integration test is opt-in, never part of the default suite',()=>{
 assert.ok(read('internal/browser/rod_proxy_auth_test.go').startsWith('//go:build browser_live\n'));
});

test('desktop startup records logs before UI startup even without a GUI console',()=>{
 const main=read('main.go');
 assert.match(main,/log\.SetOutput\(io\.MultiWriter\(applog\.Logger\{[^}]+\}, os\.Stderr\)\)/);
 assert.ok(main.indexOf('log.SetOutput(')<main.indexOf('NewApp(dataDir)'));
 assert.match(main,/Logger:\s+diskLogger/);
 const logFn=go.slice(go.indexOf('func (a *App) log('));
 assert.ok(logFn.indexOf('disk.Log(level, message)')<logFn.indexOf('a.uiStore.AppendLog(level, message)'));
 assert.match(logFn,/disk.Error\(fmt.Sprintf\("UI log persistence failed/);
});

test('process capture and fatal crash hook are installed only at desktop startup',()=>{
 const main=read('main.go'),capture=read('internal/applog/capture.go');
 assert.ok(main.indexOf('applog.CaptureOutput(')<main.indexOf('NewApp(dataDir)'));
 assert.match(main,/capture.Close\(\)/);
 assert.match(capture,/debug.SetCrashOutput\(crash, debug.CrashOptions\{\}\)/);
 assert.match(capture,/os.Stdout = outWrite/);
 assert.match(capture,/os.Stderr = errWrite/);
 assert.match(main,/LogLevelProduction:\s*logger.INFO/);
});
