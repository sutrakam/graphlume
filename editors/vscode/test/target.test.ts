import assert from 'node:assert/strict';
import { test } from 'node:test';

import { questionArguments } from '../src/core/args';
import type { Definition } from '../src/core/target';
import {
  answeredLocation,
  candidatesFrom,
  chooseDefinition,
  definitionsFrom,
  describeDefinition,
  targetFor,
  targetWarnings,
} from '../src/core/target';
import { answer } from './helpers';

const one = definitionsFrom(answer('symbol.json'));
const many = definitionsFrom(answer('symbol_many.json'));

function def(path: string, line: number, name = 'String'): Definition {
  return { name, kind: 'method', path, line, fqn: `${path}:${name}` };
}

test('definitions are read from a symbol answer', () => {
  assert.deepEqual(one, [
    { name: 'parseToolArg', kind: 'function', path: 'internal/cli/tool.go', line: 384, fqn: 'internal/cli/tool.go:parseToolArg', confidence: 'high' },
  ]);
  assert.equal(many.length, 15);
  assert.ok(many.every((d) => d.name === 'New'));
  assert.deepEqual(definitionsFrom(answer('symbol_missing.json')), []);
});

test('nothing in the index means nothing is chosen', () => {
  assert.deepEqual(chooseDefinition([], { source: 'cursor', path: 'a.go', line: 1 }), { kind: 'none' });
});

test('a single definition is taken when only the cursor position is known', () => {
  const choice = chooseDefinition(one, { source: 'cursor', path: 'internal/cli/structure_test.go', line: 254 });
  assert.deepEqual(choice, { kind: 'chosen', definition: one[0], basis: 'only-one' });
  assert.equal(chooseDefinition(one, { source: 'none' }).kind, 'chosen');
});

test('go-to-definition picks the right one of many', () => {
  const choice = chooseDefinition(many, { source: 'definition', path: 'internal/web/web.go', line: 309 });
  assert.ok(choice.kind === 'chosen');
  assert.equal(choice.basis, 'matches-hint');
  assert.equal(choice.definition.path, 'internal/web/web.go');
});

test('many definitions and no usable hint: the user chooses', () => {
  const choice = chooseDefinition(many, { source: 'cursor', path: 'cmd/graphlume/main.go', line: 10 });
  assert.ok(choice.kind === 'ask');
  assert.equal(choice.reason, 'ambiguous');
  assert.equal(choice.candidates.length, 15);
});

test('a definition the index does not have is never silently swapped for a namesake', () => {
  // The language server says the symbol lives in the standard library. The
  // index has one unrelated function with the same name.
  const outside = chooseDefinition(one, { source: 'definition', outsideProject: '/usr/local/go/src/strings/strings.go' });
  assert.ok(outside.kind === 'ask');
  assert.equal(outside.reason, 'hint-mismatch');

  // Inside the project, but in a file where the index has no such definition.
  const unindexed = chooseDefinition(one, { source: 'definition', path: 'internal/gen/zz_generated.go', line: 12 });
  assert.ok(unindexed.kind === 'ask');
  assert.equal(unindexed.reason, 'hint-mismatch');
});

test('several definitions in one file: the hinted line decides', () => {
  const defs = [def('data.go', 156), def('data.go', 201), def('other.go', 5)];
  const exact = chooseDefinition(defs, { source: 'definition', path: 'data.go', line: 201 });
  assert.ok(exact.kind === 'chosen' && exact.definition.line === 201);
  // The buffer has drifted a few lines from the index: nearest one above.
  const drifted = chooseDefinition(defs, { source: 'definition', path: 'data.go', line: 204 });
  assert.ok(drifted.kind === 'chosen' && drifted.definition.line === 201);
  const between = chooseDefinition(defs, { source: 'cursor', path: 'data.go', line: 170 });
  assert.ok(between.kind === 'chosen' && between.definition.line === 156);
  // Above every candidate, or no line at all: ask, but only about that file.
  const above = chooseDefinition(defs, { source: 'cursor', path: 'data.go', line: 3 });
  assert.ok(above.kind === 'ask' && above.candidates.length === 2);
  const noLine = chooseDefinition(defs, { source: 'cursor', path: 'data.go' });
  assert.ok(noLine.kind === 'ask' && noLine.candidates.length === 2);
});

test('a chosen definition is asked about by its name, path and line, never its qualified name', () => {
  const single = targetFor(one[0] as Definition);
  assert.deepEqual(single, { name: 'parseToolArg', path: 'internal/cli/tool.go', line: 384 });
  assert.deepEqual(questionArguments('impact', single, 'low'), {
    symbol: 'parseToolArg',
    path: 'internal/cli/tool.go',
    line: 384,
    min_confidence: 'low',
  });

  const web = many.find((d) => d.path === 'internal/web/web.go') as Definition;
  const target = targetFor(web);
  assert.deepEqual(target, { name: 'New', path: 'internal/web/web.go', line: 309 });
  assert.deepEqual(questionArguments('tests', target, 'high'), { symbol: 'New', path: 'internal/web/web.go', line: 309, min_confidence: 'high' });
  assert.deepEqual(questionArguments('callers', target, 'high'), { name: 'New', path: 'internal/web/web.go', line: 309, min_confidence: 'high' });

  // The index reported no line: the file is all there is to send.
  assert.deepEqual(targetFor({ name: 'x', kind: 'function', path: 'a.go', line: 0 }), { name: 'x', path: 'a.go' });
});

test('overloads share one qualified name, and only the line tells them apart', () => {
  const overloads = definitionsFrom(answer('symbol_overloads.json'));
  assert.equal(overloads.length, 2);
  assert.equal(overloads[0]?.fqn, overloads[1]?.fqn);
  const targets = overloads.map((d) => questionArguments('impact', targetFor(d), 'low'));
  assert.deepEqual(
    targets.map((t) => [t['symbol'], t['line']]),
    [
      ['parse', 4],
      ['parse', 8],
    ],
  );
  // The editor points at the second one: it is chosen without asking.
  const choice = chooseDefinition(overloads, { source: 'definition', path: 'java/src/main/java/com/shop/Parser.java', line: 8 });
  assert.ok(choice.kind === 'chosen' && choice.definition.line === 8);
});

test('the candidates a tool lists instead of answering are read as definitions to choose from', () => {
  assert.deepEqual(candidatesFrom(answer('callers_ambiguous.json')), [
    {
      name: 'parse',
      kind: 'method',
      path: 'java/src/main/java/com/shop/Parser.java',
      line: 4,
      fqn: 'java/src/main/java/com/shop/Parser.java:parse',
      detail: 'in class Parser, 1 parameter(s), lines 4-6',
    },
    {
      name: 'parse',
      kind: 'method',
      path: 'java/src/main/java/com/shop/Parser.java',
      line: 8,
      fqn: 'java/src/main/java/com/shop/Parser.java:parse',
      detail: 'in class Parser, 2 parameter(s), lines 8-10',
    },
  ]);
  const three = candidatesFrom(answer('impact_ambiguous.json'));
  assert.deepEqual(
    three.map((d) => [d.kind, d.path, d.line, d.detail]),
    [
      ['function', 'internal/cache/cache.go', 10, '0 parameter(s), lines 10-12'],
      ['function', 'internal/queue/queue.go', 10, '0 parameter(s), lines 10-12'],
      ['function', 'internal/store/store.go', 10, '0 parameter(s), lines 10-12'],
    ],
  );
  // Each one, sent back, is what the tool said would select it.
  assert.deepEqual(questionArguments('impact', targetFor(three[2] as Definition), 'low'), {
    symbol: 'New',
    path: 'internal/store/store.go',
    line: 10,
    min_confidence: 'low',
  });
  // Rows of an ordinary answer are results, not candidates.
  assert.deepEqual(candidatesFrom(answer('callers.json')), []);
  assert.deepEqual(candidatesFrom(answer('symbol_many.json')), []);
});

test('the answered definition is read back from the summary', () => {
  assert.deepEqual(answeredLocation(answer('callers.json').summary), { path: 'internal/cli/tool.go', line: 384 });
  assert.deepEqual(answeredLocation(answer('implementations.json').summary), { path: 'internal/capability/symbols.go', line: 19 });
  assert.deepEqual(answeredLocation(answer('callers_stale.json').summary), {
    path: 'src/main/java/com/pay/model/Transaction.java',
    line: 10,
  });
  assert.equal(answeredLocation(answer('impact.json').summary), undefined);
  assert.equal(answeredLocation(answer('tests.json').summary), undefined);
  assert.equal(answeredLocation(answer('callers_missing.json').summary), undefined);
  assert.equal(answeredLocation(''), undefined);
});

test('the answered definition is still read when the summary opens with a warning word', () => {
  // "INCOMPLETE — rows 1-50 of 63 are shown. 63 caller(s) of record (...) are recorded."
  assert.deepEqual(answeredLocation(answer('callers_paged.json').summary), { path: 'internal/handlers/record.go', line: 7 });
  assert.deepEqual(answeredLocation(answer('callers_unreliable.json').summary), { path: 'py/billing/invoice.py', line: 5 });
  assert.deepEqual(answeredLocation(answer('callers_unknown.json').summary), { path: 'rb/lib/money.rb', line: 2 });
  // impact now names the definition too.
  assert.deepEqual(answeredLocation(answer('impact_unreliable.json').summary), { path: 'py/billing/invoice.py', line: 5 });
  // A list of candidates answered for nothing.
  assert.equal(answeredLocation(answer('impact_ambiguous.json').summary), undefined);
});

test('an answer for a different definition than the one asked about is flagged', () => {
  // Captured from `callers name=New path=internal/web/web.go`: the tool
  // answered for internal/capability/ownership/ownership.go:136 instead.
  const wrong = answer('callers_wrong_target.json');
  const web = many.find((d) => d.path === 'internal/web/web.go') as Definition;
  const warnings = targetWarnings(web, many, wrong);
  assert.equal(warnings.length, 1);
  assert.match(warnings[0] as string, /is for the definition at internal\/capability\/ownership\/ownership\.go:136, NOT the one you asked about \(internal\/web\/web\.go:309\)/u);

  // The same answer is correct when ownership.go was the one asked about.
  const ownership = many.find((d) => d.path === 'internal/capability/ownership/ownership.go') as Definition;
  assert.deepEqual(targetWarnings(ownership, many, wrong), []);
});

test('no warning when the name is unique', () => {
  assert.deepEqual(targetWarnings(one[0] as Definition, one, answer('callers.json')), []);
  assert.deepEqual(targetWarnings(one[0] as Definition, one, answer('impact.json')), []);
});

test('an answer that names the definition asked about, among namesakes, carries no warning', () => {
  // Five definitions are named settle. The tool was given the path and line
  // of one and says in its summary that it answered for that one.
  const defs = [def('py/billing/invoice.py', 5, 'settle'), def('py/billing/refund.py', 5, 'settle'), def('py/billing/credit.py', 5, 'settle')];
  assert.deepEqual(targetWarnings(defs[0] as Definition, defs, answer('callers_unreliable.json')), []);
  assert.deepEqual(targetWarnings(defs[0] as Definition, defs, answer('impact_unreliable.json')), []);
  // The same answers are not about the namesake in refund.py.
  const other = targetWarnings(defs[1] as Definition, defs, answer('impact_unreliable.json'));
  assert.equal(other.length, 1);
  assert.match(other[0] as string, /is for the definition at py\/billing\/invoice\.py:5, NOT the one you asked about \(py\/billing\/refund\.py:5\)/u);
  // Two overloads in one file differ only by line.
  const overloads = definitionsFrom(answer('symbol_overloads.json'));
  assert.deepEqual(targetWarnings(overloads[1] as Definition, overloads, answer('callers_selected.json')), []);
  assert.equal(targetWarnings(overloads[0] as Definition, overloads, answer('callers_selected.json')).length, 1);
});

test('an older impact answer does not say which namesake it is for, and that is stated', () => {
  const defs = [def('data.go', 156), def('data.go', 201), def('other.go', 5)];
  const warnings = targetWarnings(defs[1] as Definition, defs, answer('impact.json'));
  assert.equal(warnings.length, 1);
  assert.match(warnings[0] as string, /3 definitions are named "String" and the tool did not say which one it answered for\. The one asked about is data\.go:201\./u);
});

test('a shared name with no answered location is stated, not hidden', () => {
  const web = many.find((d) => d.path === 'internal/web/web.go') as Definition;
  const warnings = targetWarnings(web, many, { ...answer('callers_wrong_target.json'), summary: 'something new' });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0] as string, /did not say which one it answered for/u);
});

test('a definition is described with its kind and location', () => {
  assert.equal(describeDefinition(one[0] as Definition), 'function  internal/cli/tool.go:384');
  assert.equal(describeDefinition({ name: 'x', kind: '', path: 'a.go', line: 0 }), 'a.go');
});
