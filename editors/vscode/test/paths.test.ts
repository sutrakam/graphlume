import assert from 'node:assert/strict';
import * as path from 'node:path';
import { test } from 'node:test';

import { findNameLine, findProjectRoot, resolveInsideRoot, toRepoRelative } from '../src/core/paths';

const posix = path.posix;
const win = path.win32;

test('toRepoRelative gives forward-slash paths for files inside the root only', () => {
  assert.equal(toRepoRelative('/work/repo', '/work/repo/internal/cli/tool.go', posix), 'internal/cli/tool.go');
  assert.equal(toRepoRelative('/work/repo', '/work/repo', posix), undefined);
  assert.equal(toRepoRelative('/work/repo', '/work/other/a.go', posix), undefined);
  assert.equal(toRepoRelative('/work/repo', '/work/repo-two/a.go', posix), undefined);
  assert.equal(toRepoRelative('/work/repo', '/work/repo/..hidden/a.go', posix), '..hidden/a.go');
  assert.equal(toRepoRelative('C:\\work\\repo', 'C:\\work\\repo\\internal\\cli\\tool.go', win), 'internal/cli/tool.go');
  assert.equal(toRepoRelative('C:\\work\\repo', 'D:\\elsewhere\\a.go', win), undefined);
});

test('resolveInsideRoot refuses anything that leaves the project', () => {
  assert.equal(resolveInsideRoot('/work/repo', 'internal/cli/tool.go', posix), '/work/repo/internal/cli/tool.go');
  assert.equal(resolveInsideRoot('/work/repo', 'a/../b.go', posix), '/work/repo/b.go');
  for (const bad of ['', '../secrets.txt', 'a/../../etc/passwd', '/etc/passwd', '\\\\server\\share\\x', 'C:\\Windows\\x', 'C:x', 'nul\u0000.go', '.', 'a/..']) {
    assert.equal(resolveInsideRoot('/work/repo', bad, posix), undefined, JSON.stringify(bad));
  }
  assert.equal(resolveInsideRoot('C:\\work\\repo', 'internal/cli/tool.go', win), 'C:\\work\\repo\\internal\\cli\\tool.go');
  assert.equal(resolveInsideRoot('C:\\work\\repo', '..\\other\\x.go', win), undefined);
});

test('findProjectRoot picks the nearest folder with an index, up to the workspace folder', () => {
  const indexed = new Set(['/work/mono/services/pay']);
  const has = (dir: string): boolean => indexed.has(dir);
  assert.equal(findProjectRoot('/work/mono/services/pay/src/api', '/work/mono', has, posix), '/work/mono/services/pay');
  // No index anywhere: the workspace folder, where one would be created.
  assert.equal(findProjectRoot('/work/mono/services/mail/src', '/work/mono', has, posix), '/work/mono');
  // An index above the workspace folder is not used: the search stops at the folder.
  assert.equal(findProjectRoot('/work/mono/a', '/work/mono', (d) => d === '/work', posix), '/work/mono');
  // The workspace folder itself.
  assert.equal(findProjectRoot('/work/mono', '/work/mono', (d) => d === '/work/mono', posix), '/work/mono');
  // A file outside the workspace folder never drives the search.
  assert.equal(findProjectRoot('/elsewhere/x', '/work/mono', () => true, posix), '/work/mono');
});

test('findNameLine prefers the declaration over an earlier mention', () => {
  const go = ['package cli', '', '// parseToolArg decodes one pair.', 'var x = parseToolArg', 'func parseToolArg(pair string) {', '}'].join('\n');
  assert.equal(findNameLine(go, 'parseToolArg'), 4);

  const method = ['type Server struct{}', '', 'func (s *Server) findCallers(ctx context.Context) error {', '}'].join('\n');
  assert.equal(findNameLine(method, 'findCallers'), 2);

  const java = ['class A {', '  int n = getStatus();', '  public Status getStatus() {', '  }', '}'].join('\r\n');
  assert.equal(findNameLine(java, 'getStatus'), 2);

  const python = ['import os', 'result = run()', 'def run():', '    pass'].join('\n');
  assert.equal(findNameLine(python, 'run'), 2);
});

test('findNameLine matches whole words and falls back to the first mention', () => {
  const text = ['const runner = 1;', 'rerun();', 'run();', 'run();'].join('\n');
  assert.equal(findNameLine(text, 'run'), 2);
  assert.equal(findNameLine(text, 'missing'), undefined);
  assert.equal(findNameLine(text, ''), undefined);
  // A parameter that merely has the name is not taken for the declaration.
  const param = ['function other(run) {', '}', 'function run() {', '}'].join('\n');
  assert.equal(findNameLine(param, 'run'), 2);
  // Names with characters that are special in a regular expression.
  assert.equal(findNameLine('a\nlet $x = 1;', '$x'), 1);
});
