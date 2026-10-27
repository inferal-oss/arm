// Component ownership is distinct from graph references such as c:controls.
// A field can move into a new wrapper before the previous wrapper is retired.
export function replaceOwnership(parent, children) {
  const next=new Set(children),previous=parent.ownedChildren??new Set();
  parent.ownedChildren=next;
  for(const child of next)child.parents.add(parent);
  const orphaned=[];
  for(const child of previous)if(!next.has(child)) {
    child.parents.delete(parent);
    if(!child.parents.size&&!child.retainCount&&!child.disposed)orphaned.push(child);
  }
  return orphaned;
}
export function releaseOwnership(parent) {
  for(const owner of parent.parents)owner.ownedChildren?.delete(parent);
  parent.parents.clear();
  return replaceOwnership(parent,[]);
}
