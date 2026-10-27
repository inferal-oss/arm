import {test} from 'node:test';
import assert from 'node:assert/strict';
import {formatKubeQuantity as format} from './kube-quantity.mjs';

test('Kubernetes CPU display handles canonical milli/micro/nano and decimal quantities exactly',()=>{
  for(const [raw,want]of [['1900m','1.9 cores'],['250m','0.25 cores'],['180000000000n','180 cores'],['100u','0.0001 cores'],['1','1 core'],['.5','0.5 cores'],['5.','5 cores'],['1e3','1,000 cores'],['0','0 cores'],['9223372036854775807','9,223,372,036,854,775,807 cores']])assert.equal(format(raw,'cpu'),want,raw);
});
test('Kubernetes byte display distinguishes binary and decimal suffixes and labels approximations',()=>{
  for(const [raw,want]of [['7Gi','7 GiB'],['1536Mi','1.5 GiB'],['20Gi','20 GiB'],['1024','1 KiB'],['1G','≈ 953.7 MiB'],['535328752Ki','≈ 510.5 GiB'],['1e3','1,000 B'],['400m','0.4 B'],['0','0 B'],['9223372036854775807','≈ 8 EiB']])assert.equal(format(raw,'bytes'),want,raw);
});
test('unreadable, unsupported and very small quantities retain raw text rather than inventing a value',()=>{
  for(const raw of ['-1','1MiB','1K','1e1000000','1/2','unknown',' 7Gi ','1e-12','9'.repeat(257)])assert.equal(format(raw,'cpu'),raw);
  assert.equal(format('1n','bytes'),'1n');assert.equal(format('7Gi','unknown'),'7Gi');
  assert.equal(format(null),'Not reported');assert.equal(format(''),'Not reported');
});
