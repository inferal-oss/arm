import {test} from 'node:test';
import assert from 'node:assert/strict';
import {dashboardShortcut, installDashboardShortcuts} from './dashboard-shortcuts.mjs';
import {createDashboardHistory, dashboardViews} from './route.mjs';
const event = (key, extra = {}) => ({key, target: {tagName:'BODY'}, ...extra});
test('digits map to the same eight ordered views, never arbitrary keys or modifiers', () => {
  for (let index=0; index<8; index++) assert.equal(dashboardShortcut(event(String(index+1))),dashboardViews[index]);
  for (const key of ['0','9','!','Digit1','11','1\n','',undefined]) assert.equal(dashboardShortcut(event(key)),null);
  for (const flag of ['defaultPrevented','repeat','isComposing','altKey','ctrlKey','metaKey','shiftKey'])
    for (const key of ['1','8']) assert.equal(dashboardShortcut(event(key,{[flag]:true})),null,flag);
  assert.equal(dashboardShortcut(event('1',{keyCode:229})),null);
  assert.equal(dashboardShortcut(event('1'),true),null);
  assert.equal(dashboardShortcut(event('8'),true),null);
});
test('editing paths, selects, contenteditable and custom editors retain their keystrokes', () => {
  for (const tagName of ['INPUT','TEXTAREA','SELECT','input'])
    for (const key of ['2','8']) assert.equal(dashboardShortcut(event(key,{target:{tagName}})),null);
  assert.equal(dashboardShortcut(event('2',{composedPath:()=>[{tagName:'SPAN'},{isContentEditable:true}]})),null);
  for (const value of ['', 'true','plaintext-only'])
    assert.equal(dashboardShortcut(event('2',{target:{getAttribute:key=>key==='contenteditable'?value:null}})),null);
  for (const role of ['textbox','searchbox','combobox','listbox','spinbutton'])
    assert.equal(dashboardShortcut(event('2',{target:{getAttribute:key=>key==='role'?role:null}})),null);
  assert.equal(dashboardShortcut(event('2',{target:{tagName:'BUTTON'}})),'resources');
});
test('installed shortcuts only navigate public history; visible reviews and dialogs block without side effects', () => {
  let handler, removed, visible=false, address=new URL('http://localhost/?view=resources'), pushes=0, callbacks=0, prevented=0;
  const document={addEventListener(type,fn){assert.equal(type,'keydown');handler=fn;},removeEventListener(type,fn){removed=fn;},querySelectorAll:()=>[{getClientRects:()=>visible?[{}]:[]}]};
  const location={get href(){return address.href;},get pathname(){return address.pathname;},get search(){return address.search;},get hash(){return address.hash;}};
  const routes=createDashboardHistory({location,history:{pushState(state,_,target){assert.equal(state,null);pushes++;address=new URL(target,address);},replaceState(){throw Error('unexpected replacement');}},onRoute:()=>callbacks++});
  const dispose=installDashboardShortcuts({document,selectView:view=>routes.navigate({view,lease:null})});
  handler(event('4',{preventDefault(){prevented++;}}));
  assert.equal(address.search,'?view=messages');assert.equal(pushes,1);assert.equal(callbacks,1);assert.equal(prevented,1);
  visible=true;
  handler(event('1',{preventDefault(){throw Error('blocked key consumed');}}));
  assert.equal(pushes,1);assert.equal(callbacks,1);
  visible=false;
  handler(event('4',{preventDefault(){prevented++;}}));
  assert.equal(pushes,1,'current tab must not duplicate history');
  handler(event('8',{preventDefault(){prevented++;}}));
  assert.equal(address.search,'?view=screen');assert.equal(pushes,2);assert.equal(callbacks,2);assert.equal(prevented,3);
  visible=true;handler(event('8',{preventDefault(){throw Error('blocked screen key consumed');}}));assert.equal(pushes,2);
  dispose();assert.equal(removed,handler);
});
