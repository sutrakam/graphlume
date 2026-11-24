// Checks the promises the extension makes about itself that a compiler does
// not check: no runtime dependencies, no shell, no network, the pure modules
// stay pure, and the manifest matches the code.

import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { test } from 'node:test';

import { TOOLS } from '../src/core/args';
import { CONFIDENCE_TIERS } from '../src/core/confidence';
import { EXTENSION_DIR } from './helpers';

interface Manifest {
  publisher: string;
  license: string;
  main: string;
  engines: { vscode: string };
  dependencies?: Record<string, string>;
  devDependencies: Record<string, string>;
  activationEvents: string[];
  contributes: {
    commands: { command: string; title: string }[];
    configuration: { properties: Record<string, { type: string; default: unknown; scope?: string; enum?: string[] }> };
    views: Record<string, { id: string }[]>;
    viewsContainers: Record<string, { id: string; icon: string }[]>;
  };
}

const manifest = JSON.parse(fs.readFileSync(path.join(EXTENSION_DIR, 'package.json'), 'utf8')) as Manifest;

function sources(dir: string): { file: string; text: string }[] {
  const out: { file: string; text: string }[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...sources(full));
    } else if (entry.name.endsWith('.ts')) {
      out.push({ file: path.relative(EXTENSION_DIR, full), text: fs.readFileSync(full, 'utf8') });
    }
  }
  return out;
}

const src = sources(path.join(EXTENSION_DIR, 'src'));

/** Module specifiers a file imports or requires. */
function imports(text: string): string[] {
  const found: string[] = [];
  for (const match of text.matchAll(/(?:from\s+|require\(\s*|import\(\s*)['"]([^'"]+)['"]/gu)) {
    found.push(match[1] as string);
  }
  return found;
}

test('there are no runtime dependencies, and only the agreed development ones', () => {
  assert.deepEqual(Object.keys(manifest.dependencies ?? {}), []);
  assert.deepEqual(Object.keys(manifest.devDependencies).sort(), ['@types/node', '@types/vscode', 'typescript']);
});

test('the manifest names the publisher, the licence and a compiled entry point', () => {
  assert.equal(manifest.publisher, 'graphlume');
  assert.equal(manifest.license, 'Apache-2.0');
  assert.match(manifest.engines.vscode, /^\^1\.\d+\.\d+$/u);
  assert.ok(fs.existsSync(path.join(EXTENSION_DIR, manifest.main)), `${manifest.main} is missing: compile first`);
  // The API typings must not be newer than the editor version asked for.
  const engine = manifest.engines.vscode.replace(/^\^/u, '').split('.').slice(0, 2).join('.');
  const types = (manifest.devDependencies['@types/vscode'] as string).replace(/^[~^]/u, '').split('.').slice(0, 2).join('.');
  assert.equal(types, engine);
});

test('the licence text shipped with the extension is the repository licence', () => {
  const repoLicence = path.resolve(EXTENSION_DIR, '..', '..', 'LICENSE');
  const own = fs.readFileSync(path.join(EXTENSION_DIR, 'LICENSE'), 'utf8');
  assert.match(own, /Apache License\s+Version 2\.0, January 2004/u);
  if (fs.existsSync(repoLicence)) {
    assert.equal(own, fs.readFileSync(repoLicence, 'utf8'), 'editors/vscode/LICENSE has drifted from the repository LICENSE');
  }
  // Apache-2.0 section 4(d): a distribution carries the NOTICE file too.
  const repoNotice = path.resolve(EXTENSION_DIR, '..', '..', 'NOTICE');
  const ownNotice = fs.readFileSync(path.join(EXTENSION_DIR, 'NOTICE'), 'utf8');
  if (fs.existsSync(repoNotice)) {
    assert.equal(ownNotice, fs.readFileSync(repoNotice, 'utf8'), 'editors/vscode/NOTICE has drifted from the repository NOTICE');
  }
});

test('the extension activates on its commands and on a workspace with .graphlume/', () => {
  for (const { command } of manifest.contributes.commands) {
    assert.ok(manifest.activationEvents.includes(`onCommand:${command}`), `${command} has no activation event`);
  }
  assert.ok(manifest.activationEvents.includes('workspaceContains:.graphlume'));
  assert.ok(!manifest.activationEvents.includes('*'));
  assert.ok(!manifest.activationEvents.includes('onStartupFinished'));
});

test('the four questions and the index command are contributed and registered', () => {
  const titles = Object.fromEntries(manifest.contributes.commands.map((c) => [c.command, c.title]));
  assert.equal(titles['graphlume.showCallers'], 'Show callers');
  assert.equal(titles['graphlume.showImpact'], 'Show what this could break');
  assert.equal(titles['graphlume.showTests'], 'Show tests that reach this');
  assert.equal(titles['graphlume.showImplementations'], 'Show implementations');
  assert.ok('graphlume.indexWorkspace' in titles);
  const extension = src.find((s) => s.file === path.join('src', 'extension.ts'))?.text ?? '';
  for (const { command } of manifest.contributes.commands) {
    assert.ok(extension.includes(`registerCommand('${command}'`), `${command} is not registered in extension.ts`);
  }
});

test('the settings: binary path (machine scope), CodeLens off by default, minimum confidence', () => {
  const props = manifest.contributes.configuration.properties;
  assert.deepEqual(Object.keys(props).sort(), ['graphlume.codeLens.enabled', 'graphlume.minConfidence', 'graphlume.path']);
  assert.equal(props['graphlume.path']?.default, 'graphlume');
  // Machine scope: a repository's .vscode/settings.json cannot choose the program that runs.
  assert.equal(props['graphlume.path']?.scope, 'machine');
  assert.equal(props['graphlume.codeLens.enabled']?.default, false);
  assert.equal(props['graphlume.minConfidence']?.default, 'low');
  assert.deepEqual([...(props['graphlume.minConfidence']?.enum ?? [])].sort(), [...CONFIDENCE_TIERS].sort());
});

test('the results view and its container exist, with an icon file', () => {
  const container = manifest.contributes.viewsContainers['panel']?.[0];
  assert.equal(container?.id, 'graphlume');
  assert.ok(fs.existsSync(path.join(EXTENSION_DIR, container?.icon ?? 'missing')));
  assert.deepEqual(
    manifest.contributes.views['graphlume']?.map((v) => v.id),
    ['graphlume.results'],
  );
});

test('the pure modules import neither vscode nor each other through the editor layer', () => {
  const core = src.filter((s) => s.file.startsWith(path.join('src', 'core') + path.sep));
  assert.ok(core.length >= 10);
  for (const { file, text } of core) {
    for (const specifier of imports(text)) {
      assert.notEqual(specifier, 'vscode', `${file} imports vscode`);
      assert.ok(specifier.startsWith('./') || specifier.startsWith('node:'), `${file} imports ${specifier}`);
    }
  }
});

test('only the runner starts a process, and never through a shell', () => {
  for (const { file, text } of src) {
    const usesChildProcess = imports(text).some((s) => s === 'node:child_process' || s === 'child_process');
    assert.equal(usesChildProcess, file === path.join('src', 'core', 'runner.ts'), `${file}: child_process`);
    assert.doesNotMatch(text, /shell:\s*true/u, `${file} enables a shell`);
    assert.doesNotMatch(text, /(?<![.\w])(?:exec|execSync|execFile|execFileSync|spawnSync)\s*\(/u, `${file} uses a process API other than spawn`);
    assert.doesNotMatch(text, /ShellExecution|sendText\s*\(/u, `${file} sends text to a shell`);
  }
  const runner = src.find((s) => s.file === path.join('src', 'core', 'runner.ts'))?.text ?? '';
  assert.match(runner, /shell:\s*false/u);
});

test('nothing in the extension opens a network connection', () => {
  const network = new Set(['http', 'https', 'http2', 'net', 'tls', 'dgram', 'dns', 'ws']);
  for (const { file, text } of src) {
    for (const specifier of imports(text)) {
      assert.ok(!network.has(specifier.replace(/^node:/u, '')), `${file} imports ${specifier}`);
    }
    assert.doesNotMatch(text, /\bfetch\s*\(|XMLHttpRequest|WebSocket|openExternal|createTelemetryLogger/u, `${file} reaches for the network`);
  }
});

test('every tool the extension calls is one the documentation lists', () => {
  // docs/reference/mcp-tools.md is generated from the server's own tool list.
  const reference = path.resolve(EXTENSION_DIR, '..', '..', 'docs', 'reference', 'mcp-tools.md');
  if (!fs.existsSync(reference)) {
    return;
  }
  const text = fs.readFileSync(reference, 'utf8');
  for (const tool of TOOLS) {
    assert.ok(text.includes('`' + tool + '`'), `${tool} is not in docs/reference/mcp-tools.md`);
  }
});

test('the package ignores sources, tests and dependencies', () => {
  const ignore = fs.readFileSync(path.join(EXTENSION_DIR, '.vscodeignore'), 'utf8').split(/\r?\n/u);
  for (const pattern of ['src/**', 'test/**', 'out/test/**', 'node_modules/**', '**/*.map', '*.vsix']) {
    assert.ok(ignore.includes(pattern), `.vscodeignore lacks ${pattern}`);
  }
});
