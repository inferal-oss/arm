import {test} from 'node:test';
import assert from 'node:assert/strict';
import {metricHelp} from './metric-help.mjs';

test('filesystem explanation separates human policy from collector paths and notes', () => {
  const info = metricHelp('filesystem', {
    measurements: [['Available space', {scope: 'nonprivileged-filesystem-available:/private/arm', method: 'statfs', quality: 'reported', detail: '<script>literal data</script>'}]],
    filesystem: {type: 'apfs', shared: true, detail: 'never sum volume totals as independent capacity'},
  });
  assert.match(info.paragraphs.join(' '), /APFS volumes can share/);
  assert.match(info.paragraphs.join(' '), /snapshots, clones/);
  assert.doesNotMatch(info.paragraphs.join(' '), /statfs|nonprivileged|\/private\/arm|<script>|never sum/);
  assert.ok(info.technical.some(([, value]) => value === 'nonprivileged-filesystem-available:/private/arm'));
  assert.ok(info.technical.some(([, value]) => value === '<script>literal data</script>'));
});

test('I/O, unavailable GPU and reservations have plain independent explanations', () => {
  assert.match(metricHelp('disk_io').paragraphs.join(' '), /count it twice/);
  assert.match(metricHelp('network').paragraphs.join(' '), /VPN/);
  for (const kind of ['disk_io','network']) {
    const copy=metricHelp(kind).paragraphs.join(' ');
    assert.match(copy,/sum of reported counters, not exclusive physical throughput/);
    assert.match(copy,/custom selection keeps exact device identities/);
    assert.match(copy,/gaps rather than adding zero/);
  }
  assert.match(metricHelp('gpu').paragraphs.join(' '), /does not mean the GPU is idle/);
  assert.doesNotMatch(metricHelp('resource-memory').paragraphs.join(' '), /pool follows/);
  const live = metricHelp('resource-memory', {memory: {}});
  assert.match(live.paragraphs.join(' '), /pool follows/);
  assert.match(live.paragraphs.join(' '), /more headroom than necessary/);
  for (const kind of ['cpu', 'memory', 'filesystem', 'disk_io', 'network', 'gpu', 'resource-cpu', 'resource-memory', 'resource-disk', 'resource-port', 'resource-custom']) {
    const info = metricHelp(kind);
    assert.ok(info.title && info.paragraphs.length);
    assert.deepEqual(info.technical, []);
  }
});
