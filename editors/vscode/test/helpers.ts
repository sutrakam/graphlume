import * as fs from 'node:fs';
import * as path from 'node:path';

import type { ToolAnswer } from '../src/core/result';
import { parseToolEnvelope } from '../src/core/result';

// The files in test/fixtures are real output, captured from a `graphlume`
// built at commit 6fadd51 and run against an index of this repository
// (init, index, resolve). Only two things were changed afterwards: absolute
// index paths became /work/project/.graphlume/index.db, and the project
// name became "graphlume".
//
//   <tool>.json            graphlume --json tool call <tool> ...   (exit 0)
//   callers.json            name=parseToolArg
//   callers_truncated.json  name=emitJSON                (item cap reached)
//   callers_floor.json      name=emitJSON min_confidence=exact
//   callers_missing.json    name=NoSuchSymbolAnywhere
//   callers_wrong_target.json  name=New path=internal/web/web.go
//                                (answers for ownership.go: the path hint lost)
//   callers_stale.json      fixtures/payment-platform, indexed one commit behind HEAD
//   symbol_many.json        name=New                     (15 definitions)
//   impact_omitted.json  symbol=emitJSON              (51 results left out)
//   implementations.json    name=Symbol;  _both: name=Impacted
//   error_missing_argument.json  callers with no name    (exit 1, result null)
//   error_bad_confidence.json    min_confidence=bogus         (exit 1)
//   not_indexed.json             after `init` only            (exit 0, status absent)
//   status_*.json                graphlume --json status
//   stderr_no_index.txt          tool call in a folder with no .graphlume (exit 1)
//   stderr_unknown_tool.txt      tool call nope               (exit 1)
//
// Those samples predate reliability, paging and the ambiguous-name outcome:
// they carry none of the new fields, and so they also stand for an older
// binary. The samples below were captured from a `graphlume` built from the
// working tree on top of commit a0000c5, run against a small throwaway
// project made for the purpose (Go, Java, Python and Ruby files; init, index,
// resolve). Nothing in them was edited. That project was not a git repository, so every one says
// "HEAD could not be determined" and carries the stale-index signal.
//
//   impact_ambiguous.json  symbol=New                 (exit 0: three candidates, nothing analysed)
//   callers_ambiguous.json    name=parse path=<Parser.java>   (two overloads in one file)
//   callers_selected.json     the same with line=8       (answers for the second overload)
//   symbol_overloads.json     name=parse                 (both overloads, one qualified name)
//   callers_paged.json        name=record path=... line=7     (rows 1-50 of 63)
//   callers_unreliable.json   name=settle path=... line=5     (Python: 38% of references bound)
//   impact_unreliable.json the same symbol            (UNRELIABLE, and "Risk unknown")
//   callers_unknown.json      name=cents path=... line=2      (Ruby: no reference extracted)
//
// One sample is from the v1.0.1 release, built from its tag and run against a
// copy of the same project: what a binary from before `path` and `line`
// selected a definition says when it is given them.
//
//   error_unknown_argument.json    impact symbol=record path=... line=7   (exit 1, result null)

/** The extension folder. Compiled tests live in out/test, two levels below it. */
export const EXTENSION_DIR = path.resolve(__dirname, '..', '..');

export const FIXTURE_DIR = path.join(EXTENSION_DIR, 'test', 'fixtures');

/** A captured sample, exactly as `graphlume` printed it. */
export function fixture(name: string): string {
  return fs.readFileSync(path.join(FIXTURE_DIR, name), 'utf8');
}

export function answer(name: string): ToolAnswer {
  return parseToolEnvelope(fixture(name));
}
