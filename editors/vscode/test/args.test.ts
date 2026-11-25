import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  checkSymbolName,
  expandBinaryPath,
  findSymbolArguments,
  indexStepArgv,
  indexStepsFor,
  MAX_SYMBOL_LENGTH,
  questionArguments,
  statusArgv,
  toolCallArgv,
} from '../src/core/args';
import { normalizeMinConfidence } from '../src/core/confidence';

test('a tool call is one argv array with the arguments as a single JSON value', () => {
  const argv = toolCallArgv('/work/repo', 'callers', { name: 'parseToolArg', path: 'internal/cli/tool.go', min_confidence: 'high' });
  assert.deepEqual(argv, [
    '--project=/work/repo',
    '--json',
    'tool',
    'call',
    'callers',
    '--args={"name":"parseToolArg","path":"internal/cli/tool.go","min_confidence":"high"}',
  ]);
});

test('editor text never becomes its own argument, whatever it contains', () => {
  // Each of these would be dangerous or wrong in the CLI's key=value form or
  // on a shell command line. As JSON inside one argv element they are inert.
  const hostile = ['@/etc/passwd', 'true', 'null', '42', '--project=/tmp', 'a"; rm -rf ~; "', '$(id)', '`id`', 'x y', "it's"];
  for (const name of hostile) {
    const argv = toolCallArgv('/work/repo', 'symbol', findSymbolArguments(name));
    assert.equal(argv.length, 6, name);
    assert.equal(argv[4], 'symbol');
    const last = argv[5] as string;
    assert.ok(last.startsWith('--args='), name);
    assert.deepEqual(JSON.parse(last.slice('--args='.length)), { name }, name);
  }
});

test('a project path that looks like a flag stays the value of --project', () => {
  assert.equal(toolCallArgv('--json', 'symbol', { name: 'x' })[0], '--project=--json');
  assert.deepEqual(statusArgv('/work/my repo'), ['--project=/work/my repo', '--json', 'status']);
});

test('only the tools the extension knows can be called', () => {
  assert.throws(() => toolCallArgv('/r', 'diff' as never, {}), /unknown tool/u);
});

test('every question sends the bare name, the path and line of one definition, and the floor', () => {
  const target = { name: 'New', path: 'internal/web/web.go', line: 309 };
  assert.deepEqual(questionArguments('callers', target, 'low'), { name: 'New', path: 'internal/web/web.go', line: 309, min_confidence: 'low' });
  assert.deepEqual(questionArguments('implementations', target, 'high'), {
    name: 'New',
    path: 'internal/web/web.go',
    line: 309,
    min_confidence: 'high',
  });
  // impact and tests call the name `symbol`. It is the bare name
  // there too: a qualified one is shared by overloads and selects nothing.
  assert.deepEqual(questionArguments('impact', target, 'medium'), {
    symbol: 'New',
    path: 'internal/web/web.go',
    line: 309,
    min_confidence: 'medium',
  });
  assert.deepEqual(questionArguments('tests', target, 'low'), { symbol: 'New', path: 'internal/web/web.go', line: 309, min_confidence: 'low' });
});

test('the line travels as a JSON number, inside the one JSON argument', () => {
  const argv = toolCallArgv('/work/repo', 'impact', questionArguments('impact', { name: 'New', path: 'internal/web/web.go', line: 309 }, 'low'));
  assert.equal(argv.length, 6);
  assert.equal(argv[5], '--args={"symbol":"New","path":"internal/web/web.go","line":309,"min_confidence":"low"}');
});

test('a line is sent only when it is a real line and its file is known', () => {
  assert.deepEqual(questionArguments('implementations', { name: 'Symbol' }, 'high'), { name: 'Symbol', min_confidence: 'high' });
  assert.deepEqual(questionArguments('tests', { name: 'parseToolArg', path: 'internal/cli/tool.go' }, 'low'), {
    symbol: 'parseToolArg',
    path: 'internal/cli/tool.go',
    min_confidence: 'low',
  });
  for (const line of [0, -4, 2.5, Number.NaN]) {
    assert.deepEqual(questionArguments('callers', { name: 'f', path: 'a.go', line }, 'low'), { name: 'f', path: 'a.go', min_confidence: 'low' });
  }
  // A line with no file would select a definition on that line in any file.
  assert.deepEqual(questionArguments('callers', { name: 'f', line: 12 }, 'low'), { name: 'f', min_confidence: 'low' });
  assert.deepEqual(questionArguments('callers', { name: 'f', path: '', line: 12 }, 'low'), { name: 'f', min_confidence: 'low' });
});

test('symbol names are checked before use', () => {
  assert.deepEqual(checkSymbolName('  parseToolArg '), { ok: true, name: 'parseToolArg' });
  assert.equal(checkSymbolName('').ok, false);
  assert.equal(checkSymbolName('   ').ok, false);
  assert.equal(checkSymbolName('two words').ok, false);
  assert.equal(checkSymbolName('line\nbreak').ok, false);
  assert.equal(checkSymbolName('nul\u0000byte').ok, false);
  assert.equal(checkSymbolName('=>').ok, false);
  assert.equal(checkSymbolName('x'.repeat(MAX_SYMBOL_LENGTH + 1)).ok, false);
  assert.equal(checkSymbolName('x'.repeat(MAX_SYMBOL_LENGTH)).ok, true);
  // Identifiers outside ASCII are real identifiers.
  assert.equal(checkSymbolName('größe').ok, true);
  assert.equal(checkSymbolName('$scope').ok, true);
});

test('the index steps depend on whether a database exists', () => {
  assert.deepEqual(indexStepsFor('absent'), ['init', 'index', 'resolve']);
  assert.deepEqual(indexStepsFor(undefined), ['init', 'index', 'resolve']);
  assert.deepEqual(indexStepsFor('empty'), ['index', 'resolve']);
  assert.deepEqual(indexStepsFor('ready'), ['index', 'resolve']);
  assert.deepEqual(indexStepArgv('/work/repo', 'resolve'), ['--project=/work/repo', 'resolve']);
});

test('the binary path expands a leading tilde and nothing else', () => {
  assert.equal(expandBinaryPath('', '/home/u'), 'graphlume');
  assert.equal(expandBinaryPath('graphlume', '/home/u'), 'graphlume');
  assert.equal(expandBinaryPath('~/go/bin/graphlume', '/home/u'), '/home/u/go/bin/graphlume');
  assert.equal(expandBinaryPath('~/go/bin/graphlume', '/home/u/'), '/home/u/go/bin/graphlume');
  assert.equal(expandBinaryPath('/opt/$HOME/graphlume', '/home/u'), '/opt/$HOME/graphlume');
  assert.equal(expandBinaryPath('~other/bin/graphlume', '/home/u'), '~other/bin/graphlume');
});

test('the minimum confidence setting falls back to low', () => {
  assert.equal(normalizeMinConfidence('HIGH'), 'high');
  assert.equal(normalizeMinConfidence(' exact '), 'exact');
  assert.equal(normalizeMinConfidence('bogus'), 'low');
  assert.equal(normalizeMinConfidence(undefined), 'low');
  assert.equal(normalizeMinConfidence(3), 'low');
});
