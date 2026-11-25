import assert from 'node:assert/strict';
import { test } from 'node:test';

import { AbortedError } from '../src/core/cache';
import { GraphLumeClient, STATUS_TIMEOUT_MS, TOOL_TIMEOUT_MS } from '../src/core/client';
import { QUESTION_TOOL, questionArguments } from '../src/core/args';
import { messageForAmbiguous, messageForError, messageForOutcome } from '../src/core/messages';
import { interpretToolRun, isAmbiguous } from '../src/core/result';
import type { RunRequest, RunResult } from '../src/core/runner';
import { BinaryNotFoundError, BinaryNotRunnableError, RunCancelledError, RunTimeoutError, runBinary } from '../src/core/runner';
import { StatusError } from '../src/core/status';
import { answeredLocation, definitionsFrom, targetFor } from '../src/core/target';
import { answer, fixture } from './helpers';

function fakeRunner(reply: (request: RunRequest) => RunResult | Error): { runner: (r: RunRequest) => Promise<RunResult>; requests: RunRequest[] } {
  const requests: RunRequest[] = [];
  return {
    requests,
    runner: async (request) => {
      requests.push(request);
      const result = reply(request);
      if (result instanceof Error) {
        throw result;
      }
      return result;
    },
  };
}

test('callTool runs the configured binary in the project with a timeout and the caller signal', async () => {
  const { runner, requests } = fakeRunner(() => ({ code: 0, stdout: fixture('callers.json'), stderr: '' }));
  let binary = '/opt/bin/graphlume';
  const client = new GraphLumeClient(runner, () => binary);
  const controller = new AbortController();
  const call = await client.callTool('/work/repo', 'callers', { name: 'parseToolArg' }, { signal: controller.signal });

  assert.equal(call.outcome.kind, 'answer');
  assert.deepEqual(call.argv, ['--project=/work/repo', '--json', 'tool', 'call', 'callers', '--args={"name":"parseToolArg"}']);
  assert.equal(requests.length, 1);
  assert.equal(requests[0]?.binary, '/opt/bin/graphlume');
  assert.equal(requests[0]?.cwd, '/work/repo');
  assert.equal(requests[0]?.timeoutMs, TOOL_TIMEOUT_MS);
  assert.equal(requests[0]?.signal, controller.signal);
  assert.deepEqual(requests[0]?.args, call.argv);

  // The setting is read on every call, so a change takes effect at once.
  binary = '/usr/local/bin/graphlume';
  await client.callTool('/work/repo', 'tests', { symbol: 'x' }, { timeoutMs: 1234 });
  assert.equal(requests[1]?.binary, '/usr/local/bin/graphlume');
  assert.equal(requests[1]?.timeoutMs, 1234);
});

test('status parses a status, and reports a failure with the CLI message', async () => {
  const ok = fakeRunner(() => ({ code: 0, stdout: fixture('status_ready.json'), stderr: '' }));
  const status = await new GraphLumeClient(ok.runner, () => 'graphlume').status('/work/repo');
  assert.equal(status.indexStatus, 'ready');
  assert.deepEqual(ok.requests[0]?.args, ['--project=/work/repo', '--json', 'status']);
  assert.equal(ok.requests[0]?.timeoutMs, STATUS_TIMEOUT_MS);

  const bad = fakeRunner(() => ({ code: 1, stdout: '', stderr: 'error: unable to open database file\n' }));
  await assert.rejects(new GraphLumeClient(bad.runner, () => 'graphlume').status('/work/repo'), (err: unknown) => {
    assert.ok(err instanceof StatusError);
    assert.equal(err.message, 'unable to open database file');
    return true;
  });
});

test('a missing binary surfaces as its own error with a settings remedy', async () => {
  const { runner } = fakeRunner(() => new BinaryNotFoundError('graphlume'));
  const client = new GraphLumeClient(runner, () => 'graphlume');
  await assert.rejects(client.callTool('/work/repo', 'symbol', { name: 'x' }), BinaryNotFoundError);
  const message = messageForError(new BinaryNotFoundError('graphlume'));
  assert.equal(message?.severity, 'error');
  assert.equal(message?.remedy, 'open-settings');
  assert.match(message?.text ?? '', /binary was not found \(tried "graphlume"\)/u);
  assert.match(message?.text ?? '', /graphlume\.path/u);
});

test('messages: not runnable, timeout, cancellation and unknown errors', () => {
  assert.equal(messageForError(new BinaryNotRunnableError('/x', 'EACCES: permission denied'))?.remedy, 'open-settings');
  const timeout = messageForError(new RunTimeoutError(20_000));
  assert.equal(timeout?.severity, 'warning');
  assert.match(timeout?.text ?? '', /did not answer within 20 seconds and was stopped/u);
  // Cancelling is the user's own action and needs no message.
  assert.equal(messageForError(new RunCancelledError()), undefined);
  assert.equal(messageForError(new AbortedError()), undefined);
  assert.equal(messageForError(new Error('odd'))?.text, 'GraphLume: odd');
  assert.equal(messageForError('a string')?.text, 'GraphLume: a string');
});

test('messages: no index offers to build one; a tool failure offers the report', () => {
  const noIndex = interpretToolRun({ code: 1, stdout: '', stderr: fixture('stderr_no_index.txt') });
  assert.ok(noIndex.kind === 'no-index');
  const m1 = messageForOutcome(noIndex);
  assert.equal(m1.remedy, 'index');
  assert.match(m1.text, /this workspace has no index to answer from\. no index at /u);

  const shaped = interpretToolRun({ code: 0, stdout: fixture('not_indexed.json'), stderr: '' });
  assert.ok(shaped.kind === 'no-index');
  assert.equal(messageForOutcome(shaped).remedy, 'index');

  const failed = interpretToolRun({ code: 1, stdout: fixture('error_bad_confidence.json'), stderr: '' });
  assert.ok(failed.kind === 'tool-failure');
  const m2 = messageForOutcome(failed);
  assert.equal(m2.remedy, 'show-report');
  assert.equal(m2.severity, 'error');
  assert.match(m2.text, /the tool callers reported a failure\. unknown confidence "bogus"/u);

  const unknownTool = interpretToolRun({ code: 1, stdout: '', stderr: fixture('stderr_unknown_tool.txt') });
  assert.ok(unknownTool.kind === 'cli-error');
  const m3 = messageForOutcome(unknownTool);
  assert.match(m3.text, /no tool named "nope"/u);
  assert.match(m3.text, /may be older than this extension expects/u);
});

test('messages: a binary too old to take path and line is named as the likely cause', () => {
  // Captured from the v1.0.1 release, asked the way this extension now asks.
  const refused = interpretToolRun({ code: 1, stdout: fixture('error_unknown_argument.json'), stderr: '' });
  assert.ok(refused.kind === 'tool-failure');
  const message = messageForOutcome(refused);
  assert.equal(message.severity, 'error');
  assert.equal(message.remedy, 'show-report');
  assert.match(message.text, /the tool impact reported a failure\. validating "arguments": .*unexpected additional properties \["line" "path"\]/u);
  assert.match(message.text, /The installed graphlume may be older than this extension expects; update it\.$/u);
  // Any other failure is reported without that guess.
  const other = interpretToolRun({ code: 1, stdout: fixture('error_bad_confidence.json'), stderr: '' });
  assert.ok(other.kind === 'tool-failure');
  assert.doesNotMatch(messageForOutcome(other).text, /older than this extension/u);
});

test('messages: an ambiguous name is a warning that carries the tool summary and offers the report', () => {
  const message = messageForAmbiguous('New', answer('impact_ambiguous.json'));
  assert.equal(message.severity, 'warning');
  assert.equal(message.remedy, 'show-report');
  assert.equal(
    message.text,
    'GraphLume: "New" matches several definitions, so nothing was analysed. ' +
      'AMBIGUOUS — "New" names 3 definitions and impact answered for none of them. Pass the path and line of one row below.',
  );
  assert.equal(
    messageForAmbiguous('New', { ...answer('impact_ambiguous.json'), summary: '' }).text,
    'GraphLume: "New" matches several definitions, so nothing was analysed.',
  );
});

// Runs against a real binary and a real index when both are named:
//   GRAPHLUME_BIN=/path/to/graphlume GRAPHLUME_PROJECT=/path/to/indexed/repo npm test
const bin = process.env['GRAPHLUME_BIN'];
const project = process.env['GRAPHLUME_PROJECT'];
const live = bin !== undefined && bin !== '' && project !== undefined && project !== '';

test('live: the real binary answers every question the extension asks', { skip: live ? false : 'set GRAPHLUME_BIN and GRAPHLUME_PROJECT' }, async () => {
  const client = new GraphLumeClient(runBinary, () => bin as string);
  const root = project as string;

  const status = await client.status(root);
  assert.ok(['ready', 'degraded'].includes(status.indexStatus), `index status is ${status.indexStatus}`);

  const overview = await client.callTool(root, 'overview', {});
  assert.ok(overview.outcome.kind === 'answer');
  assert.ok(overview.outcome.answer.provenance !== undefined);

  const symbol = process.env['GRAPHLUME_SYMBOL'] ?? 'parseToolArg';
  const found = await client.callTool(root, 'symbol', { name: symbol });
  assert.ok(found.outcome.kind === 'answer');
  assert.ok(found.outcome.answer.items.length >= 1, `no definition of ${symbol}`);

  // Asked the way the extension asks: the bare name, with the path and line
  // of one definition symbol listed. That must select it, however many
  // definitions share the name.
  const definition = definitionsFrom(found.outcome.answer)[0];
  assert.ok(definition !== undefined, `no located definition of ${symbol}`);
  for (const kind of ['callers', 'impact', 'tests', 'implementations'] as const) {
    const tool = QUESTION_TOOL[kind];
    const call = await client.callTool(root, tool, questionArguments(kind, targetFor(definition), 'low'));
    assert.equal(call.outcome.kind, 'answer', `${tool}: ${JSON.stringify(call.outcome).slice(0, 300)}`);
    assert.ok(call.outcome.kind === 'answer');
    assert.equal(call.outcome.answer.tool, tool);
    assert.notEqual(call.outcome.answer.summary, '');
    assert.ok(call.outcome.answer.provenance?.caveat, `${tool} carried no provenance caveat`);
    assert.equal(isAmbiguous(call.outcome.answer), false, `${tool} listed candidates: ${call.outcome.answer.summary}`);
    assert.deepEqual(answeredLocation(call.outcome.answer.summary), { path: definition.path, line: definition.line }, tool);
  }

  // A name the CLI's key=value form would have read as a file or a boolean.
  for (const odd of ['@/etc/passwd', 'true', '--help']) {
    const call = await client.callTool(root, 'symbol', { name: odd });
    assert.ok(call.outcome.kind === 'answer', `${odd}: ${JSON.stringify(call.outcome).slice(0, 200)}`);
    assert.equal(call.outcome.answer.items.length, 0);
  }

  // A failure the tool reports: exit 1, is_error true.
  const failure = await client.callTool(root, 'callers', { name: symbol, min_confidence: 'bogus' });
  assert.equal(failure.outcome.kind, 'tool-failure');
});

test('live: a folder with no index is reported as such', { skip: live ? false : 'set GRAPHLUME_BIN and GRAPHLUME_PROJECT' }, async () => {
  const os = await import('node:os');
  const fs = await import('node:fs');
  const path = await import('node:path');
  const empty = fs.mkdtempSync(path.join(os.tmpdir(), 'rc-vscode-noindex-'));
  try {
    const client = new GraphLumeClient(runBinary, () => bin as string);
    assert.equal((await client.status(empty)).indexStatus, 'absent');
    const call = await client.callTool(empty, 'callers', { name: 'x' });
    assert.equal(call.outcome.kind, 'no-index');
    // Asking must not create an index directory as a side effect.
    assert.deepEqual(fs.readdirSync(empty), []);
  } finally {
    fs.rmSync(empty, { recursive: true, force: true });
  }
});
