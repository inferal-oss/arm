import test from 'node:test';
import assert from 'node:assert/strict';
import {replaceOwnership,releaseOwnership} from './graph-ownership.mjs';
const node=()=>({parents:new Set(),ownedChildren:new Set(),retainCount:0,disposed:false});
test('replacement retires only abandoned children, preserving reused identity',()=>{
 const root=node(),old=node(),keep=node(),next=node();replaceOwnership(root,[old,keep]);
 assert.deepEqual(replaceOwnership(root,[keep,next]),[old]);assert.equal(keep.parents.has(root),true);assert.equal(old.parents.size,0);assert.equal(next.parents.has(root),true);
});
test('a field reparented into a successor wrapper survives old wrapper retirement',()=>{
 const first=node(),successor=node(),field=node();replaceOwnership(first,[field]);replaceOwnership(successor,[field]);
 assert.deepEqual(releaseOwnership(first),[]);assert.deepEqual([...field.parents],[successor]);assert.deepEqual(releaseOwnership(successor),[field]);
});
test('retained detached views survive navigation gaps until explicitly released',()=>{
 const from=node(),to=node(),view=node();view.retainCount=1;replaceOwnership(from,[view]);assert.deepEqual(replaceOwnership(from,[]),[]);
 replaceOwnership(to,[view]);view.retainCount--;assert.equal(view.parents.has(to),true);assert.deepEqual(releaseOwnership(to),[view]);
});
test('separate dialog body and action ownership both retire without double owning descendants',()=>{
 const dialog=node(),body=node(),button=node(),field=node();replaceOwnership(body,[field]);replaceOwnership(dialog,[body,button]);
 assert.deepEqual(releaseOwnership(dialog),[body,button]);assert.equal(field.parents.has(body),true);assert.equal(field.parents.has(dialog),false);assert.deepEqual(releaseOwnership(body),[field]);
});
