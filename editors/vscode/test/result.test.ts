import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  AMBIGUOUS_NAME,
  doubtOf,
  EnvelopeError,
  failureMessage,
  interpretToolRun,
  isAmbiguous,
  limitsOf,
  parseToolEnvelope,
  rowsWithheld,
  verdictIsWarning,
  verdictLine,
} from '../src/core/result';
import { answer, fixture } from './helpers';

test('callers: every item keeps its tier, location and reason', () => {
  const a = answer('callers.json');
  assert.equal(a.tool, 'callers');
  assert.equal(a.isError, false);
  assert.equal(a.summary, '3 caller(s) of parseToolArg (internal/cli/tool.go:384).');
  assert.equal(a.indexStatus, 'ready');
  assert.equal(a.indexRevision, 1);
  assert.equal(a.items.length, 3);
  assert.deepEqual(a.items[2], {
    kind: 'calls',
    name: 'newToolCallCmd',
    path: 'internal/cli/tool.go',
    line: 323,
    confidence: 'high',
    source: 'source_code',
    why: 'defined in the same file',
  });
  assert.equal(a.truncated, false);
  assert.equal(a.verdict?.status, 'strong');
  assert.deepEqual(a.verdict?.counts, { high: 3 });
  assert.equal(a.provenance?.headMatchesBuild, true);
  assert.match(a.provenance?.caveat ?? '', /commits only/u);
  assert.match(a.text, /^=== SYSTEM METADATA ===/u);
});

test('impact: indirect rows have no line, and the tool says what it could not see', () => {
  const a = answer('impact.json');
  assert.deepEqual(
    a.items.map((i) => [i.kind, i.name, i.line]),
    [
      ['direct', 'TestParseToolArgTypes', 254],
      ['direct', 'newToolCallCmd', 323],
      ['indirect', 'newToolCmd', undefined],
      ['indirect', 'NewRoot', undefined],
    ],
  );
  assert.equal(a.truncated, true);
  assert.match(a.truncatedReason ?? '', /depth limit 3 reached/u);
  const limits = limitsOf(a);
  const texts = limits.map((l) => l.text);
  // Nothing the tool said is dropped: one truncation, every note, the caveat.
  assert.equal(limits.length, 1 + a.notes.length + 1);
  assert.ok(texts.some((t) => t.startsWith('NOT VISIBLE (unexpanded_frontier)')));
  assert.ok(texts.some((t) => t.startsWith('NOT VISIBLE (unresolved_references)')));
  assert.ok(texts.some((t) => t.startsWith('This is a LOWER BOUND.')));
  for (const note of a.notes) {
    assert.ok(texts.includes(note), note);
  }
  assert.equal(limits[0]?.kind, 'truncated');
  assert.equal(limits.at(-1)?.kind, 'caveat');
});

test('impact: results left out to fit the response are reported with their breakdown', () => {
  const a = answer('impact_omitted.json');
  assert.equal(a.items.length, 50);
  assert.deepEqual(a.omitted, {
    count: 51,
    byKind: [
      { label: 'indirect', count: 49 },
      { label: 'direct', count: 2 },
    ],
    byConfidence: [{ label: 'high', count: 51 }],
    weakestShown: 'high',
    strongerDropped: [],
  });
  const omitted = limitsOf(a).find((l) => l.kind === 'omitted');
  assert.ok(omitted);
  assert.match(omitted.text, /^51 result\(s\) were left out/u);
  assert.match(omitted.text, /indirect 49, direct 2/u);
});

test('tests and implementations parse their own item kinds', () => {
  const tests = answer('tests.json');
  assert.equal(tests.items.length, 10);
  assert.ok(tests.items.every((i) => i.kind === 'test' && i.line === undefined && i.confidence === 'high'));
  assert.equal(tests.truncated, true);

  const impl = answer('implementations.json');
  assert.equal(impl.items.length, 5);
  assert.ok(impl.items.every((i) => i.kind === 'subtype' && i.confidence === 'medium'));
  assert.equal(impl.verdict?.status, 'weak');
  assert.deepEqual(impl.verdict?.signals, ['all 5 results are inferred (5 medium, 0 low); none was compiler-proven']);
  assert.ok(limitsOf(impl).some((l) => l.kind === 'signal'));

  const both = answer('implementations_both.json');
  assert.deepEqual(
    both.items.map((i) => i.kind),
    ['subtype', 'supertype'],
  );
});

test('a stale index is the first limit, in the tool own words', () => {
  const a = answer('callers_stale.json');
  assert.equal(a.provenance?.headMatchesBuild, false);
  const limits = limitsOf(a);
  assert.equal(limits[0]?.kind, 'stale');
  assert.equal(limits[0]?.warning, true);
  assert.match(limits[0]?.text ?? '', /^The index was built from 0571f90c but HEAD is d28fecc3\./u);
  // The caveat is shown once, not again at the end.
  assert.equal(limits.filter((l) => l.text === a.provenance?.caveat).length, 1);
  assert.ok(limits.some((l) => l.kind === 'signal' && /does not match HEAD/u.test(l.text)));
});

test('a confidence floor is reported by the tool and passed through', () => {
  const a = answer('callers_floor.json');
  assert.equal(a.items.length, 0);
  assert.match(a.notes[0] ?? '', /^Filtered to confidence "exact" and above: 55 lower-confidence result\(s\) were omitted/u);
  assert.ok(limitsOf(a).some((l) => l.warning && /omitted/u.test(l.text)));
});

test('exit 0 with items is an answer', () => {
  const outcome = interpretToolRun({ code: 0, stdout: fixture('callers.json'), stderr: '' });
  assert.equal(outcome.kind, 'answer');
});

test('exit 0 with nothing found is still an answer', () => {
  const outcome = interpretToolRun({ code: 0, stdout: fixture('callers_missing.json'), stderr: '' });
  assert.equal(outcome.kind, 'answer');
  assert.ok(outcome.kind === 'answer' && outcome.answer.items.length === 0);
});

test('exit 1 with is_error is a tool failure carrying the tool message', () => {
  const bad = interpretToolRun({ code: 1, stdout: fixture('error_bad_confidence.json'), stderr: '' });
  assert.equal(bad.kind, 'tool-failure');
  assert.ok(bad.kind === 'tool-failure');
  assert.match(bad.message, /^unknown confidence "bogus": use exact, high, medium or low/u);
  assert.match(bad.message, /hint: /u);
  assert.doesNotMatch(bad.message, /SYSTEM METADATA|error_code/u);

  // A schema rejection has `result: null` and one bare line of text.
  const missing = interpretToolRun({ code: 1, stdout: fixture('error_missing_argument.json'), stderr: '' });
  assert.ok(missing.kind === 'tool-failure');
  assert.equal(missing.answer.hasResult, false);
  assert.match(missing.message, /missing properties: \["name"\]/u);
});

test('an ambiguous name is an answer, not a failure, and its rows are candidates', () => {
  const outcome = interpretToolRun({ code: 0, stdout: fixture('impact_ambiguous.json'), stderr: '' });
  assert.ok(outcome.kind === 'answer');
  const a = outcome.answer;
  assert.equal(a.isError, false);
  assert.equal(a.outcome, AMBIGUOUS_NAME);
  assert.equal(isAmbiguous(a), true);
  assert.match(a.summary, /^AMBIGUOUS — "New" names 3 definitions and impact answered for none of them\./u);
  assert.deepEqual(
    a.items.map((i) => [i.kind, i.name, i.path, i.line, i.confidence]),
    [
      ['candidate', 'New', 'internal/cache/cache.go', 10, undefined],
      ['candidate', 'New', 'internal/queue/queue.go', 10, undefined],
      ['candidate', 'New', 'internal/store/store.go', 10, undefined],
    ],
  );
  // Nothing was analysed, so nothing is graded.
  assert.equal(a.verdict, undefined);
  assert.equal(a.reliability, undefined);
  assert.equal(a.page, undefined);
  assert.equal(doubtOf(a), undefined);

  assert.equal(isAmbiguous(answer('callers_ambiguous.json')), true);
  assert.equal(isAmbiguous(answer('callers_selected.json')), false);
});

test('reliability is read with its level, lead and reasons', () => {
  const a = answer('callers_unreliable.json');
  assert.equal(a.reliability?.level, 'low');
  assert.equal(a.reliability?.lead, 'only 38% of the Python references that name something this index defines are bound.');
  assert.equal(a.reliability?.reasons.length, 2);
  assert.match(a.reliability?.reasons[0] ?? '', /^COVERAGE TOO LOW — relationship lookup for Python is unreliable here/u);
  assert.equal(a.verdict?.status, 'unreliable');
  assert.match(a.summary, /^UNRELIABLE — only 38% of the Python references/u);
  assert.deepEqual(doubtOf(a), { word: 'unreliable', from: 'reliability' });

  const none = answer('callers_unknown.json');
  assert.equal(none.reliability?.level, 'none');
  assert.match(none.summary, /^UNKNOWN — GraphLume found no reference in any Ruby file of this index/u);
  assert.deepEqual(doubtOf(none), { word: 'unknown', from: 'reliability' });

  // A paged list from a well-covered language: the coverage is good and the
  // verdict alone says the rows are not all of them.
  const paged = answer('callers_paged.json');
  assert.deepEqual(paged.reliability, { level: 'good', lead: '', reasons: [] });
  assert.equal(paged.verdict?.status, 'incomplete');
  assert.deepEqual(doubtOf(paged), { word: 'incomplete', from: 'verdict' });
});

test('a doubtful answer: any level but good, and the unreliable, incomplete and mixed verdicts', () => {
  const base = answer('callers.json');
  assert.equal(doubtOf(base), undefined);
  const withLevel = (level: string) => doubtOf({ ...base, reliability: { level, lead: '', reasons: [] } });
  assert.equal(withLevel('good'), undefined);
  assert.deepEqual(withLevel('incomplete'), { word: 'incomplete', from: 'reliability' });
  assert.deepEqual(withLevel('unverified'), { word: 'unverified', from: 'reliability' });
  assert.deepEqual(withLevel('low'), { word: 'unreliable', from: 'reliability' });
  assert.deepEqual(withLevel('none'), { word: 'unknown', from: 'reliability' });
  // A level this extension has never heard of is not a good one.
  assert.deepEqual(withLevel('partial'), { word: 'partial', from: 'reliability' });

  const withVerdict = (status: string) => doubtOf({ ...base, verdict: { status, counts: {}, signals: [], nextStep: '' } });
  assert.equal(withVerdict('proven'), undefined);
  assert.equal(withVerdict('strong'), undefined);
  assert.deepEqual(withVerdict('unreliable'), { word: 'unreliable', from: 'verdict' });
  assert.deepEqual(withVerdict('incomplete'), { word: 'incomplete', from: 'verdict' });
  assert.deepEqual(withVerdict('mixed'), { word: 'mixed confidence', from: 'verdict' });

  for (const status of ['weak', 'empty', 'unreliable', 'incomplete', 'mixed']) {
    assert.equal(verdictIsWarning(status), true, status);
  }
  for (const status of ['proven', 'strong', '', 'toString']) {
    assert.equal(verdictIsWarning(status), false, status);
  }
});

test('a page is read with its counted total, and says how many rows were withheld', () => {
  const paged = answer('callers_paged.json');
  assert.deepEqual(paged.page, { offset: 0, limit: 50, total: 63, returned: 50, nextOffset: 50 });
  assert.equal(paged.items.length, 50);
  assert.equal(rowsWithheld(paged), 13);
  assert.equal(paged.truncated, true);
  assert.match(paged.truncatedReason ?? '', /^showing rows 1-50 of 63 caller\(s\), so 13 are not shown here\./u);

  // A page that reaches the end has no next_offset.
  const whole = answer('callers_unreliable.json');
  assert.deepEqual(whole.page, { offset: 0, limit: 50, total: 1, returned: 1, nextOffset: 0 });
  assert.equal(rowsWithheld(whole), 0);
});

test('an older binary: none of the new fields, and nothing is made of their absence', () => {
  for (const name of [
    'callers.json',
    'callers_truncated.json',
    'callers_floor.json',
    'callers_missing.json',
    'callers_wrong_target.json',
    'callers_stale.json',
    'impact.json',
    'impact_omitted.json',
    'tests.json',
    'implementations.json',
    'implementations_both.json',
    'symbol.json',
    'symbol_many.json',
  ]) {
    const a = answer(name);
    assert.equal(a.reliability, undefined, name);
    assert.equal(a.page, undefined, name);
    assert.equal(a.outcome, undefined, name);
    assert.equal(isAmbiguous(a), false, name);
    assert.equal(rowsWithheld(a), 0, name);
    assert.ok(!limitsOf(a).some((l) => l.kind === 'reliability'), name);
    // The one thing read differently in an older answer is a verdict of
    // "mixed", which is now a warning. No other sample is doubted.
    assert.deepEqual(doubtOf(a), a.verdict?.status === 'mixed' ? { word: 'mixed confidence', from: 'verdict' } : undefined, name);
  }
  assert.equal(answer('implementations_both.json').verdict?.status, 'mixed');
  // The key set of an older answer is exactly what it was.
  assert.deepEqual(Object.keys(answer('callers.json')).sort(), [
    'hasResult',
    'indexRevision',
    'indexStatus',
    'isError',
    'items',
    'notes',
    'provenance',
    'summary',
    'text',
    'tool',
    'truncated',
    'verdict',
  ]);
});

test('the reliability comes first among the limits, and a sentence the tool repeated is shown once', () => {
  const a = answer('callers_unreliable.json');
  const limits = limitsOf(a);
  assert.deepEqual(limits[0], {
    kind: 'reliability',
    label: 'Reliability',
    text: 'low — only 38% of the Python references that name something this index defines are bound.',
    warning: true,
  });
  // Both reasons are among the notes already, so they are not listed again.
  assert.equal(limits.filter((l) => l.kind === 'reliability').length, 1);
  for (const reason of a.reliability?.reasons ?? []) {
    assert.equal(limits.filter((l) => l.text === reason).length, 1, reason);
    assert.equal(limits.find((l) => l.text === reason)?.warning, true, reason);
  }
  // The verdict repeats the lead as a signal: once is enough.
  assert.ok(a.verdict?.signals.includes(a.reliability?.lead ?? ''));
  assert.ok(!limits.some((l) => l.kind === 'signal' && l.text === a.reliability?.lead));
  // Every note is still there, verbatim.
  for (const note of a.notes) {
    assert.ok(limits.some((l) => l.kind === 'note' && l.text === note), note);
  }

  // A reason the notes do not carry is shown in its own row.
  const extra = limitsOf({ ...a, reliability: { level: 'low', lead: '', reasons: ['said nowhere else'] } });
  assert.deepEqual(
    extra.filter((l) => l.kind === 'reliability').map((l) => l.text),
    ['low', 'said nowhere else'],
  );
});

test('no index: the CLI error on stderr, and the success-shaped "absent" answer', () => {
  const cli = interpretToolRun({ code: 1, stdout: '', stderr: fixture('stderr_no_index.txt') });
  assert.equal(cli.kind, 'no-index');
  assert.ok(cli.kind === 'no-index');
  assert.match(cli.message, /^no index at .*run 'graphlume init' and 'graphlume index' first$/u);

  const shaped = interpretToolRun({ code: 0, stdout: fixture('not_indexed.json'), stderr: '' });
  assert.equal(shaped.kind, 'no-index');
  assert.ok(shaped.kind === 'no-index');
  assert.match(shaped.message, /^No index has been built for this project yet/u);
  assert.match(shaped.message, /Run 'graphlume index'/u);
});

test('a CLI failure that is not about the index keeps its message', () => {
  const unknown = interpretToolRun({ code: 1, stdout: '', stderr: fixture('stderr_unknown_tool.txt') });
  assert.deepEqual(unknown, {
    kind: 'cli-error',
    message: 'no tool named "nope" on the "full" profile. Run \'graphlume tool list\' to see every tool.',
    exitCode: 1,
  });
  const silent = interpretToolRun({ code: 3, stdout: '', stderr: '' });
  assert.ok(silent.kind === 'cli-error');
  assert.match(silent.message, /status 3 and printed nothing/u);
  const garbage = interpretToolRun({ code: 0, stdout: 'not json', stderr: '' });
  assert.equal(garbage.kind, 'cli-error');
});

test('malformed envelopes are rejected, and wrong field types are ignored rather than trusted', () => {
  assert.throws(() => parseToolEnvelope('{'), EnvelopeError);
  assert.throws(() => parseToolEnvelope('[]'), EnvelopeError);
  assert.throws(() => parseToolEnvelope('{"tool":"x"}'), EnvelopeError);

  const odd = parseToolEnvelope(
    JSON.stringify({
      tool: 'callers',
      is_error: false,
      text: 7,
      result: {
        summary: ['not', 'a', 'string'],
        index_revision: '1',
        items: [null, 'x', { kind: 'calls', name: 5, line: -3, path: 'a.go', confidence: 'high' }, { kind: 'calls', line: 2.5 }],
        notes: ['kept', 9, ''],
        truncated: 'yes',
        provenance: { head_matches_build: 'true' },
        omitted: { count: 0 },
        reliability: { level: 3, lead: 'ignored', reasons: 'x' },
        page: { offset: 0, limit: 50, total: '63', returned: 50, next_offset: 50 },
        outcome: ['ambiguous_name'],
      },
    }),
  );
  assert.equal(odd.reliability, undefined);
  // A page with no counted total is no page: the rows are counted as before.
  assert.equal(odd.page, undefined);
  assert.equal(odd.outcome, undefined);
  assert.equal(isAmbiguous(odd), false);

  const partial = parseToolEnvelope(
    JSON.stringify({
      tool: 'callers',
      is_error: false,
      text: '',
      result: {
        items: [{ kind: 'calls', name: 'a' }],
        reliability: { level: 'low', lead: 7, reasons: ['kept', 3] },
        page: { total: 9, offset: -1, limit: 'many', next_offset: 2.5 },
        outcome: 'something_new',
      },
    }),
  );
  assert.deepEqual(partial.reliability, { level: 'low', lead: '', reasons: ['kept'] });
  assert.deepEqual(partial.page, { offset: 0, limit: 0, total: 9, returned: 1, nextOffset: 0 });
  assert.equal(rowsWithheld(partial), 8);
  // An outcome this extension does not know is kept and is not the ambiguous one.
  assert.equal(partial.outcome, 'something_new');
  assert.equal(isAmbiguous(partial), false);
  assert.equal(odd.text, '');
  assert.equal(odd.summary, '');
  assert.equal(odd.indexRevision, 0);
  assert.deepEqual(odd.items, [{ kind: 'calls', path: 'a.go', confidence: 'high' }, { kind: 'calls' }]);
  assert.deepEqual(odd.notes, ['kept']);
  assert.equal(odd.truncated, false);
  // Anything but a literal true means "not known to match".
  assert.equal(odd.provenance?.headMatchesBuild, false);
  assert.equal(odd.omitted, undefined);
});

test('the failure message never comes back empty', () => {
  const blank = parseToolEnvelope('{"tool":"x","is_error":true,"text":"","result":null}');
  assert.equal(failureMessage(blank), 'The tool reported a failure and gave no message.');
});

test('the verdict line lists tiers strongest first', () => {
  assert.equal(verdictLine({ status: 'mixed', counts: { low: 2, exact: 1, high: 4 }, signals: [], nextStep: '' }), 'mixed (exact 1, high 4, low 2)');
  assert.equal(verdictLine({ status: 'empty', counts: {}, signals: [], nextStep: '' }), 'empty');
});
