import {readFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {resolve} from 'node:path';
const digest = bytes => createHash('sha256').update(bytes).digest('hex');

// Fail closed if a locked runtime package, integrity pin, or audited notice changes.
export function runtimeNotices(root = import.meta.dirname) {
 const lock = JSON.parse(readFileSync(resolve(root, 'package-lock.json')));
 const manifest = JSON.parse(readFileSync(resolve(root, 'licenses/runtime-manifest.json')));
 const expected = Object.entries(lock.packages).filter(([path,p]) => path && !p.dev).map(([path]) => path.slice('node_modules/'.length)).sort();
 const actual = manifest.packages.map(p => p.package).sort();
 if (JSON.stringify(expected) !== JSON.stringify(actual)) throw Error('Runtime license inventory differs from lock');
 if (JSON.stringify(manifest.build_helpers?.map(p=>p.package)) !== JSON.stringify(['vite'])) throw Error('Bundled helper license inventory differs');
 const notices = [];
 for (const record of [...manifest.packages,...manifest.build_helpers]) {
  const locked = lock.packages['node_modules/' + record.package];
  if (record.version !== locked.version || record.integrity !== locked.integrity || record.resolved !== locked.resolved || record.declared_license !== locked.license || !Object.keys(record.files).length) throw Error('Runtime license pin mismatch: ' + record.package);
  for (const [path,sha] of Object.entries(record.files)) {
   const directory = manifest.packages.includes(record) ? 'runtime' : 'build';
   if (!path.startsWith('licenses/' + directory + '/' + record.package + '/') || path.includes('..') || !/^[a-f0-9]{64}$/.test(sha)) throw Error('Invalid runtime notice path/hash');
   const bytes = readFileSync(resolve(root,path));
   if (digest(bytes) !== sha) throw Error('Runtime notice hash mismatch: ' + path);
   notices.push(`${record.package}@${record.version} (${record.declared_license})\n${path}\n\n${bytes.toString('utf8')}`);
  }
 }
 return notices.join('\n\n');
}

export function embeddedNotices(root = import.meta.dirname) {
 const upstream = ['THIRD-PARTY-NOTICES.md','third_party/semblance/LICENSE-MIT','third_party/semblance/LICENSE-APACHE'];
 return upstream.map(path => `${path}\n\n${readFileSync(resolve(root,path),'utf8')}`).join('\n\n') + '\n\n' + runtimeNotices(root) + '\n';
}
