import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { LimitRow, ResultRow } from '../src/core/present';
import { buildResultModel, countRows, fullReport, limitLine } from '../src/core/present';
import { answer } from './helpers';

function results(model: ReturnType<typeof buildResultModel>): ResultRow[] {
  return model.sections.flatMap((s) => s.rows).filter((r): r is ResultRow => r.type === 'result');
}

function limits(model: ReturnType<typeof buildResultModel>): LimitRow[] {
  return model.sections.flatMap((s) => s.rows).filter((r): r is LimitRow => r.type === 'limit');
}

test('callers: every row states its tier and location', () => {
  const a = answer('callers.json');
  const model = buildResultModel(a, { kind: 'callers', symbol: 'parseToolArg', askedAbout: 'function  internal/cli/tool.go:384' });
  assert.equal(model.title, 'Callers of parseToolArg (function  internal/cli/tool.go:384)');
  assert.equal(model.summary, '3 caller(s) of parseToolArg (internal/cli/tool.go:384).');
  assert.equal(model.resultCount, 3);
  assert.equal(model.totalCount, 3);
  assert.equal(model.status, '3 rows');
  const rows = results(model);
  assert.deepEqual(
    rows.map((r) => [r.label, r.description, r.confidence, r.path, r.line]),
    [
      ['TestParseToolArgTypes', 'high · internal/cli/structure_test.go:254', 'high', 'internal/cli/structure_test.go', 254],
      ['TestParseToolArgTypes', 'high · internal/cli/structure_test.go:264', 'high', 'internal/cli/structure_test.go', 264],
      ['newToolCallCmd', 'high · internal/cli/tool.go:323', 'high', 'internal/cli/tool.go', 323],
    ],
  );
  assert.match(rows[2]?.tooltip ?? '', /Why: defined in the same file/u);
  assert.match(rows[2]?.tooltip ?? '', /^high: inferred by GraphLume/u);
  assert.equal(model.sections[1]?.title, 'Call sites (3)');
});

test('the limits section comes first and carries every statement the tool made', () => {
  const a = answer('impact.json');
  const model = buildResultModel(a, { kind: 'impact', symbol: 'parseToolArg' });
  const first = model.sections[0];
  assert.ok(first);
  assert.match(first.title, /^What GraphLume says about this answer \(\d+\)$/u);
  assert.equal(first.warning, true);
  const texts = limits(model).map((l) => l.text);
  assert.ok(texts.includes(a.truncatedReason as string));
  for (const note of a.notes) {
    assert.ok(texts.includes(note), note);
  }
  assert.ok(texts.includes(a.provenance?.caveat as string));
  assert.ok(texts.some((t) => t.startsWith('strong (high 4). No result fell below high confidence.')));
  assert.ok(limits(model).every((l) => l.author === 'tool'));
});

test('impact: rows are grouped by kind in the order the tool returned them', () => {
  const model = buildResultModel(answer('impact.json'), { kind: 'impact', symbol: 'parseToolArg' });
  assert.deepEqual(
    model.sections.slice(1).map((s) => s.title),
    ['Direct: proven call edge to this symbol (2)', 'Indirect: reaches this symbol through other calls (2)'],
  );
  const indirect = results(model).filter((r) => r.kind === 'indirect');
  assert.equal(indirect[0]?.description, 'high · internal/cli/tool.go');
  assert.equal(indirect[0]?.line, undefined);
  assert.match(indirect[0]?.tooltip ?? '', /GraphLume reported no line for this row/u);
});

test('implementations: subtypes and supertypes are separate groups, medium tier shown as medium', () => {
  const model = buildResultModel(answer('implementations_both.json'), { kind: 'implementations', symbol: 'Impacted' });
  assert.deepEqual(
    model.sections.slice(1).map((s) => s.title),
    ['Implements or extends this (1)', 'This implements or extends (1)'],
  );
  assert.deepEqual(
    results(model).map((r) => r.confidence),
    ['high', 'medium'],
  );
  assert.equal(model.title, 'Implementations of Impacted');
});

test('tests: ten rows, none dropped', () => {
  const model = buildResultModel(answer('tests.json'), { kind: 'tests', symbol: 'parseToolArg' });
  assert.equal(results(model).length, 10);
  assert.equal(model.sections[1]?.title, 'Tests (10)');
  assert.equal(model.title, 'Tests that reach parseToolArg');
});

test('an empty answer still shows why it may be empty', () => {
  const a = answer('callers_wrong_target.json');
  const model = buildResultModel(a, { kind: 'callers', symbol: 'New' });
  assert.equal(results(model).length, 0);
  assert.equal(model.sections.length, 1);
  assert.ok(limits(model).some((l) => /LOWER BOUND, not proof there are none/u.test(l.text) && l.warning));
});

test('extension warnings come first and are marked as the extension own words', () => {
  const model = buildResultModel(answer('callers_wrong_target.json'), {
    kind: 'callers',
    symbol: 'New',
    extensionWarnings: ['This answer is for another definition.'],
  });
  const all = limits(model);
  assert.deepEqual(all[0], { type: 'limit', label: 'Check', text: 'This answer is for another definition.', warning: true, author: 'extension' });
  assert.ok(all.slice(1).every((l) => l.author === 'tool'));
});

test('a row with no confidence says so instead of inventing one', () => {
  const a = answer('callers.json');
  const stripped = { ...a, items: [{ kind: 'calls', name: 'x', path: 'a.go', line: 2 }] };
  const row = results(buildResultModel(stripped, { kind: 'callers', symbol: 'y' }))[0];
  assert.equal(row?.description, 'no tier · a.go:2');
  assert.equal(row?.confidence, '');
  assert.match(row?.tooltip ?? '', /reported no confidence tier/u);
});

test('the full report holds the exact command and the tool text verbatim', () => {
  const a = answer('callers.json');
  const model = buildResultModel(a, { kind: 'callers', symbol: 'parseToolArg', extensionWarnings: ['Extension note.'] });
  const report = fullReport(model, a, ['--project=/work/repo', '--json', 'tool', 'call', 'callers', '--args={"name":"parseToolArg"}']);
  assert.ok(report.includes('  --project=/work/repo\n  --json\n  tool\n  call\n  callers\n  --args={"name":"parseToolArg"}\n'));
  assert.ok(report.includes('Notes from the extension (not from GraphLume):\n  - Extension note.'));
  assert.ok(report.includes(a.text.trimEnd()));
});

test('a limit is one labelled line, and a note the tool already labelled is not labelled twice', () => {
  assert.equal(limitLine({ label: 'Truncated', text: 'depth limit 3 reached' }), 'Truncated: depth limit 3 reached');
  const model = buildResultModel(answer('implementations.json'), { kind: 'implementations', symbol: 'Symbol' });
  const lines = limits(model).map(limitLine);
  assert.ok(lines.includes("Note: 5 result(s) are medium or low confidence (heuristic inference with higher error rates). Pass min_confidence='high' to restrict to compiler-verified and high-precision edges."));
  assert.ok(!lines.some((line) => line.startsWith('Note: Note:')));
  // Indented risk factors keep their label.
  const impact = limits(buildResultModel(answer('impact.json'), { kind: 'impact', symbol: 'parseToolArg' })).map(limitLine);
  assert.ok(impact.some((line) => line.startsWith('Note: public_api (+0):')));
});

test('an older answer: the status is the row count and nothing else', () => {
  const status = (name: string, kind: 'callers' | 'impact' | 'tests' | 'implementations') =>
    buildResultModel(answer(name), { kind, symbol: 'x' }).status;
  assert.equal(status('callers.json', 'callers'), '3 rows');
  assert.equal(status('callers_truncated.json', 'callers'), '50 rows');
  assert.equal(status('callers_floor.json', 'callers'), '0 rows');
  assert.equal(status('impact.json', 'impact'), '4 rows');
  assert.equal(status('tests.json', 'tests'), '10 rows');
  assert.equal(status('implementations.json', 'implementations'), '5 rows');
  // Its verdict is "mixed", which is now a warning whichever binary said it.
  assert.equal(status('implementations_both.json', 'implementations'), 'mixed confidence · 2 rows');
  assert.equal(countRows({ ...answer('callers.json'), items: [{ kind: 'calls' }] }), '1 row');
  // No row is added to an older answer.
  const model = buildResultModel(answer('callers.json'), { kind: 'callers', symbol: 'parseToolArg' });
  assert.deepEqual(
    limits(model).map((l) => l.label),
    ['Note', 'Index', 'Verdict'],
  );
});

test('a paged answer says how many rows exist and how many are shown', () => {
  const a = answer('callers_paged.json');
  const model = buildResultModel(a, { kind: 'callers', symbol: 'record' });
  assert.equal(model.resultCount, 50);
  assert.equal(model.totalCount, 63);
  assert.equal(model.status, 'incomplete · showing 50 of 63');
  assert.equal(model.summary, 'INCOMPLETE — rows 1-50 of 63 are shown. 63 caller(s) of record (internal/handlers/record.go:7) are recorded.');
  assert.equal(results(model).length, 50);
  const all = limits(model);
  // The tool's own sentence, and the extension's about what this view can do.
  assert.ok(all.some((l) => l.author === 'tool' && l.label === 'Truncated' && /^showing rows 1-50 of 63 caller\(s\)/u.test(l.text)));
  const shown = all.find((l) => l.label === 'Shown');
  assert.equal(shown?.author, 'extension');
  assert.match(shown?.text ?? '', /^This view holds 50 of the 63 rows GraphLume counted\./u);
  const verdict = all.find((l) => l.label === 'Verdict');
  assert.match(verdict?.text ?? '', /^incomplete \(high 50\)\./u);
  assert.equal(verdict?.warning, true);

  // A total with nothing withheld is stated plainly.
  assert.equal(countRows(answer('callers_unreliable.json')), '1 row');
  assert.equal(countRows(answer('callers_unknown.json')), '0 rows');
  // A next offset says rows follow even when the total does not.
  assert.equal(countRows({ ...a, page: { offset: 0, limit: 50, total: 50, returned: 50, nextOffset: 50 } }), 'showing 50, more exist');
});

test('an unreliable answer is marked in the status, first among the limits, and in the verdict', () => {
  const a = answer('callers_unreliable.json');
  const model = buildResultModel(a, { kind: 'callers', symbol: 'settle' });
  assert.equal(model.status, 'unreliable · 1 row');
  // The summary is shown as the tool wrote it, warning word included.
  assert.equal(model.summary, a.summary);
  assert.match(model.summary, /^UNRELIABLE — /u);
  const first = model.sections[0];
  assert.equal(first?.warning, true);
  assert.deepEqual(first?.rows[0], {
    type: 'limit',
    label: 'Reliability',
    text: 'low — only 38% of the Python references that name something this index defines are bound.',
    warning: true,
    author: 'tool',
  });
  const all = limits(model);
  assert.ok(all.some((l) => l.label === 'Note' && l.warning && l.text.startsWith('COVERAGE TOO LOW')));
  const verdict = all.find((l) => l.label === 'Verdict');
  assert.match(verdict?.text ?? '', /^unreliable \(high 1\)\. Do not present these rows as the answer\./u);
  assert.equal(verdict?.warning, true);
  // The row is still shown, with its tier: it is a lead.
  assert.deepEqual(
    results(model).map((r) => [r.label, r.description]),
    [['apply', 'high · py/billing/invoice.py:3']],
  );

  assert.equal(buildResultModel(answer('callers_unknown.json'), { kind: 'callers', symbol: 'cents' }).status, 'unknown · 0 rows');
});

test('the mixed, incomplete and unreliable verdicts are warnings, as weak and empty were', () => {
  const base = answer('callers.json');
  const verdictRow = (status: string) =>
    limits(buildResultModel({ ...base, verdict: { status, counts: {}, signals: [], nextStep: '' } }, { kind: 'callers', symbol: 'x' })).find(
      (l) => l.label === 'Verdict',
    );
  for (const status of ['mixed', 'incomplete', 'unreliable', 'weak', 'empty']) {
    assert.equal(verdictRow(status)?.warning, true, status);
  }
  for (const status of ['strong', 'proven']) {
    assert.equal(verdictRow(status)?.warning, false, status);
  }
  const model = buildResultModel({ ...base, verdict: { status: 'mixed', counts: {}, signals: [], nextStep: '' } }, { kind: 'callers', symbol: 'x' });
  assert.equal(model.status, 'mixed confidence · 3 rows');
});

test('impact: an unknown risk is shown in the tool own words and never reworded', () => {
  const a = answer('impact_unreliable.json');
  const model = buildResultModel(a, { kind: 'impact', symbol: 'settle' });
  assert.equal(model.summary, a.summary);
  assert.match(model.summary, /^UNRELIABLE — .* settle \(py\/billing\/invoice\.py:5\): 1 direct, 0 indirect\. Risk unknown: 3 of the 8 reference\(s\) spelled like this name are not listed as its callers/u);
  assert.equal(model.status, 'unreliable · 1 row');
  // Nothing the extension writes calls this low risk or safe.
  const own = [model.title, model.status, ...model.sections.map((s) => s.title), ...limits(model).filter((l) => l.author === 'extension').map((l) => l.text)];
  for (const text of own) {
    assert.doesNotMatch(text, /\b(low risk|risk low|safe)\b/iu, text);
  }
  // The tool's own account of why the level is unknown is passed through.
  assert.ok(limits(model).some((l) => l.text.startsWith('  unbound_references (+0): The callers of this target could not be determined')));
});

test('candidates are never grouped under a result heading', () => {
  // The extension asks the user to choose before it shows anything. If a
  // candidate list reached the view anyway, the heading says what it is.
  const model = buildResultModel(answer('impact_ambiguous.json'), { kind: 'impact', symbol: 'New' });
  assert.deepEqual(
    model.sections.filter((s) => s.rows.some((r) => r.type === 'result')).map((s) => s.title),
    ['Definitions that share the name: nothing was analysed (3)'],
  );
  assert.match(model.summary, /^AMBIGUOUS — /u);
});
