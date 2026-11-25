import assert from 'node:assert/strict';
import * as os from 'node:os';
import * as path from 'node:path';
import { test } from 'node:test';

import {
  BinaryNotFoundError,
  BinaryNotRunnableError,
  OutputTooLargeError,
  runBinary,
  RunCancelledError,
  RunTimeoutError,
} from '../src/core/runner';

// These tests start real processes. Node itself stands in for the binary.
const node = process.execPath;
const cwd = os.tmpdir();

test('runBinary returns stdout, stderr and the exit status', async () => {
  const result = await runBinary({
    binary: node,
    args: ['-e', 'process.stdout.write("out"); process.stderr.write("err"); process.exit(1)'],
    cwd,
    timeoutMs: 10_000,
  });
  assert.deepEqual(result, { code: 1, stdout: 'out', stderr: 'err' });
});

test('runBinary passes arguments through untouched: no shell reads them', async () => {
  const hostile = ['$(echo pwned)', '`echo pwned`', '; echo pwned', '&& echo pwned', '| cat', '> /tmp/x', 'a b', '"quoted"', "it's", '*', '--args={"name":"@/etc/passwd"}'];
  const result = await runBinary({
    binary: node,
    args: ['-e', 'process.stdout.write(JSON.stringify(process.argv.slice(1)))', '--', ...hostile],
    cwd,
    timeoutMs: 10_000,
  });
  assert.equal(result.code, 0);
  assert.deepEqual(JSON.parse(result.stdout), hostile);
});

test('runBinary runs in the given directory', async () => {
  const result = await runBinary({ binary: node, args: ['-e', 'process.stdout.write(process.cwd())'], cwd, timeoutMs: 10_000 });
  // macOS reports /private/var for /var; compare the last segment.
  assert.equal(path.basename(result.stdout), path.basename(cwd));
});

test('a missing binary is reported as not found', async () => {
  const missing = path.join(os.tmpdir(), 'graphlume-does-not-exist-' + String(process.pid));
  await assert.rejects(runBinary({ binary: missing, args: [], cwd, timeoutMs: 5_000 }), (err: unknown) => {
    assert.ok(err instanceof BinaryNotFoundError);
    assert.equal(err.binary, missing);
    return true;
  });
  await assert.rejects(
    runBinary({ binary: 'graphlume-not-on-path-' + String(process.pid), args: [], cwd, timeoutMs: 5_000 }),
    BinaryNotFoundError,
  );
});

test('a missing working directory is not mistaken for a missing binary', async () => {
  await assert.rejects(
    runBinary({ binary: node, args: ['-e', '0'], cwd: path.join(os.tmpdir(), 'no-such-dir-' + String(process.pid)), timeoutMs: 5_000 }),
    BinaryNotRunnableError,
  );
});

test('a run that takes too long is stopped', async () => {
  const started = Date.now();
  await assert.rejects(
    runBinary({ binary: node, args: ['-e', 'setTimeout(() => {}, 60000)'], cwd, timeoutMs: 300 }),
    RunTimeoutError,
  );
  assert.ok(Date.now() - started < 10_000);
});

test('a run can be cancelled, before it starts and while it runs', async () => {
  const before = new AbortController();
  before.abort();
  await assert.rejects(runBinary({ binary: node, args: ['-e', '0'], cwd, timeoutMs: 5_000, signal: before.signal }), RunCancelledError);

  const during = new AbortController();
  const running = runBinary({
    binary: node,
    args: ['-e', 'setTimeout(() => {}, 60000)'],
    cwd,
    timeoutMs: 30_000,
    signal: during.signal,
  });
  setTimeout(() => during.abort(), 200);
  const started = Date.now();
  await assert.rejects(running, RunCancelledError);
  assert.ok(Date.now() - started < 10_000);
});

test('output over the limit stops the run', async () => {
  await assert.rejects(
    runBinary({
      binary: node,
      args: ['-e', 'const chunk = "x".repeat(65536); setInterval(() => process.stdout.write(chunk), 1)'],
      cwd,
      timeoutMs: 20_000,
      maxOutputBytes: 200_000,
    }),
    OutputTooLargeError,
  );
});
