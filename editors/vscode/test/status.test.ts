import assert from 'node:assert/strict';
import { test } from 'node:test';

import { formatAge, indexToken, isQueryable, parseStatus, presentStatus, StatusError } from '../src/core/status';
import { answer, fixture } from './helpers';

const READY_BUILT = Date.parse('2026-11-12T14:27:34+05:30');

test('status: a ready index', () => {
  const s = parseStatus(fixture('status_ready.json'));
  assert.equal(s.indexStatus, 'ready');
  assert.equal(s.indexRun, 1);
  assert.equal(s.files, 724);
  assert.equal(s.symbols, 10743);
  assert.equal(s.relationships, 20640);
  assert.equal(s.lastIndexed, '2026-11-12T14:27:34+05:30');
  assert.equal(s.gitRevision, '6fadd51b76b45129669a667d0a93a45050be12ae');
  assert.equal(isQueryable(s), true);
});

test('status: no database, and a database with nothing indexed', () => {
  const absent = parseStatus(fixture('status_absent.json'));
  assert.equal(absent.indexStatus, 'absent');
  assert.equal(absent.lastIndexed, undefined);
  assert.equal(isQueryable(absent), false);

  const empty = parseStatus(fixture('status_empty.json'));
  assert.equal(empty.indexStatus, 'empty');
  assert.equal(isQueryable(empty), false);
  assert.equal(isQueryable(undefined), false);
});

test('status: output that is not a status is rejected', () => {
  assert.throws(() => parseStatus('nope'), StatusError);
  assert.throws(() => parseStatus('{"project":"x"}'), StatusError);
});

test('the index token changes when the index is rebuilt', () => {
  const ready = parseStatus(fixture('status_ready.json'));
  const rebuilt = { ...ready, indexRun: 2, lastIndexed: '2026-11-12T15:00:00+05:30' };
  assert.notEqual(indexToken(ready), indexToken(rebuilt));
  assert.equal(indexToken(ready), indexToken({ ...ready }));
  assert.notEqual(indexToken(ready), indexToken(undefined));
});

test('the index token also changes when the index is updated in place', () => {
  // An incremental `graphlume index` keeps the run id, and up to 1.0.1 its
  // timestamp too. `resolve` keeps both in every version.
  const ready = parseStatus(fixture('status_ready.json'));
  const before = indexToken(ready, { modifiedMs: 1_000_000, size: 4096 });
  assert.notEqual(before, indexToken(ready, { modifiedMs: 1_060_000, size: 4096 }));
  assert.notEqual(before, indexToken(ready, { modifiedMs: 1_000_000, size: 8192 }));
  assert.equal(before, indexToken(ready, { modifiedMs: 1_000_000.4, size: 4096 }));
});

test('status bar: an index updated in place shows the age of the update, and says where it came from', () => {
  const status = parseStatus(fixture('status_ready.json'));
  const provenance = answer('overview.json').provenance;
  assert.ok(provenance);
  const now = READY_BUILT + 3 * 86_400_000;
  // Built three days ago, updated by an incremental index four minutes ago.
  const view = presentStatus({ kind: 'status', status, provenance, indexFile: { modifiedMs: now - 4 * 60_000, size: 1 } }, now);
  assert.equal(view.text, 'GraphLume: 4m old');
  assert.ok(view.detail.includes('GraphLume reports its index run last finished 2026-11-12T14:27:34+05:30.'));
  assert.ok(view.detail.some((line) => /^The index file was last written .*\(4m old\)\. That is later than the run timestamp, which `resolve` never moves/u.test(line)));

  // Written within a minute of the run: that is the run itself, so its timestamp stands.
  const same = presentStatus({ kind: 'status', status, provenance, indexFile: { modifiedMs: READY_BUILT + 5_000, size: 1 } }, now);
  assert.equal(same.text, 'GraphLume: 3d old');
  assert.ok(same.detail.includes('GraphLume reports its index run last finished 2026-11-12T14:27:34+05:30 (3d old).'));
  assert.ok(!same.detail.some((line) => line.startsWith('The index file was last written')));
});

test('ages are coarse', () => {
  assert.equal(formatAge(0), 'just now');
  assert.equal(formatAge(59_000), 'just now');
  assert.equal(formatAge(5 * 60_000), '5m old');
  assert.equal(formatAge(3 * 3_600_000), '3h old');
  assert.equal(formatAge(47 * 3_600_000), '47h old');
  assert.equal(formatAge(72 * 3_600_000), '3d old');
  assert.equal(formatAge(-5), 'unknown age');
});

test('status bar: an index built from the checked-out commit', () => {
  const status = parseStatus(fixture('status_ready.json'));
  const provenance = answer('overview.json').provenance;
  assert.ok(provenance);
  const view = presentStatus({ kind: 'status', status, provenance }, READY_BUILT + 5 * 60_000);
  assert.equal(view.text, 'GraphLume: 5m old');
  assert.equal(view.severity, 'ok');
  assert.equal(view.suggestIndex, false);
  assert.ok(view.detail.includes('724 files, 10743 symbols, 20640 relationships indexed.'));
  // The limit of the comparison is always stated, even when the commits match.
  assert.ok(view.detail.some((line) => /commits only/u.test(line)));
});

test('status bar: an index built from another commit says so and suggests re-indexing', () => {
  const status = parseStatus(fixture('status_stale.json'));
  const provenance = answer('overview_stale.json').provenance;
  assert.ok(provenance);
  assert.equal(provenance.headMatchesBuild, false);
  const view = presentStatus({ kind: 'status', status, provenance }, Date.parse(status.lastIndexed ?? '') + 2 * 86_400_000);
  assert.equal(view.text, 'GraphLume: not at HEAD, 2d old');
  assert.equal(view.severity, 'warning');
  assert.equal(view.suggestIndex, true);
  assert.ok(view.detail.some((line) => /^The index was built from 0571f90c but HEAD is d28fecc3\./u.test(line)));
});

test('status bar: with no HEAD comparison it says the comparison is missing', () => {
  const status = parseStatus(fixture('status_ready.json'));
  const view = presentStatus({ kind: 'status', status }, READY_BUILT + 60_000);
  assert.equal(view.text, 'GraphLume: 1m old');
  assert.ok(view.detail.includes('Whether the index matches HEAD could not be determined.'));
});

test('status bar: no index, nothing indexed, binary missing, no folder, failure', () => {
  const absent = presentStatus({ kind: 'status', status: parseStatus(fixture('status_absent.json')) }, 0);
  assert.equal(absent.text, 'GraphLume: no index');
  assert.equal(absent.suggestIndex, true);

  const empty = presentStatus({ kind: 'status', status: parseStatus(fixture('status_empty.json')) }, 0);
  assert.equal(empty.text, 'GraphLume: not indexed');
  assert.equal(empty.suggestIndex, true);

  const missing = presentStatus({ kind: 'binary-missing', binary: '/opt/nope/graphlume' }, 0);
  assert.equal(missing.text, 'GraphLume: binary not found');
  assert.equal(missing.severity, 'error');
  assert.ok(missing.detail.some((line) => line.includes('/opt/nope/graphlume')));
  assert.ok(missing.detail.some((line) => line.includes('graphlume.path')));

  assert.equal(presentStatus({ kind: 'no-workspace' }, 0).text, 'GraphLume: no folder');
  assert.equal(presentStatus({ kind: 'checking' }, 0).text, 'GraphLume: checking');
  const failed = presentStatus({ kind: 'error', message: 'database is locked' }, 0);
  assert.equal(failed.severity, 'error');
  assert.ok(failed.detail.includes('database is locked'));
});

test('status bar: indexing, failed, degraded and unknown lifecycle states', () => {
  const ready = parseStatus(fixture('status_ready.json'));
  assert.equal(presentStatus({ kind: 'status', status: { ...ready, indexStatus: 'indexing' } }, 0).text, 'GraphLume: indexing');
  const failed = presentStatus({ kind: 'status', status: { ...ready, indexStatus: 'failed' } }, 0);
  assert.equal(failed.text, 'GraphLume: index failed');
  assert.equal(failed.severity, 'error');

  const degraded = presentStatus({ kind: 'status', status: { ...ready, indexStatus: 'degraded', parseFailures: 3 } }, READY_BUILT);
  assert.equal(degraded.text, 'GraphLume: degraded, just now');
  assert.equal(degraded.severity, 'warning');
  assert.ok(degraded.detail.some((line) => /3 file\(s\) failed to parse/u.test(line)));

  const unknown = presentStatus({ kind: 'status', status: { ...ready, indexStatus: 'quantum' } }, READY_BUILT);
  assert.equal(unknown.text, 'GraphLume: quantum, just now');
  assert.equal(unknown.severity, 'warning');
});
