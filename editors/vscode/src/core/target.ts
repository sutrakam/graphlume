// Picks which definition a question is about, and checks afterwards that the
// tool answered for that one.
//
// A name is not a symbol: fifteen functions can be called `New`. Every tool
// takes a name plus the path and line of one definition, and answers for that
// one or for none: when they do not come down to one it lists the candidates
// instead. An older binary could answer for a namesake, so the answer is
// still checked, and when it may be for a different definition than the one
// the user pointed at, the extension says so instead of presenting it as the
// answer.

import type { QuestionTarget } from './args';
import type { ToolAnswer } from './result';

/** One definition, as symbol reports it. */
export interface Definition {
  readonly name: string;
  readonly kind: string;
  /** Repository-relative, forward slashes. */
  readonly path: string;
  /** 1-based. 0 when the tool reported none. */
  readonly line: number;
  readonly fqn?: string;
  readonly confidence?: string;
  /** What tells it from its namesakes, when the tool said: `in class Gson, 2 parameter(s), lines 850-852`. */
  readonly detail?: string;
}

/** The definitions in a symbol answer that carry a location. */
export function definitionsFrom(answer: ToolAnswer): Definition[] {
  const out: Definition[] = [];
  for (const item of answer.items) {
    if (item.name === undefined || item.path === undefined) {
      continue;
    }
    const def: { name: string; kind: string; path: string; line: number; fqn?: string; confidence?: string } = {
      name: item.name,
      kind: item.kind,
      path: item.path,
      line: item.line ?? 0,
    };
    if (item.fqn !== undefined) def.fqn = item.fqn;
    if (item.confidence !== undefined) def.confidence = item.confidence;
    out.push(def);
  }
  return out;
}

/**
 * The definitions a tool listed instead of answering (`outcome:
 * ambiguous_name`), for the user to choose from.
 *
 * A candidate row has the kind `candidate`. What the definition is comes in
 * its `why`, ahead of the arguments that select it:
 * `method, in class Gson, 2 parameter(s), lines 850-852; select with path=... line=850`.
 */
export function candidatesFrom(answer: ToolAnswer): Definition[] {
  const out: Definition[] = [];
  for (const item of answer.items) {
    if (item.kind !== 'candidate' || item.name === undefined || item.path === undefined) {
      continue;
    }
    const described = (item.why ?? '').split('; select with ')[0] ?? '';
    const comma = described.indexOf(', ');
    const def: { name: string; kind: string; path: string; line: number; fqn?: string; detail?: string } = {
      name: item.name,
      kind: (comma >= 0 ? described.slice(0, comma) : described).trim(),
      path: item.path,
      line: item.line ?? 0,
    };
    if (item.fqn !== undefined) def.fqn = item.fqn;
    if (comma >= 0) def.detail = described.slice(comma + 2).trim();
    out.push(def);
  }
  return out;
}

/**
 * Where the editor believes the symbol is defined.
 *
 * `source` says how much the hint can be trusted. 'definition' comes from the
 * language server's go-to-definition and names the real definition. 'cursor'
 * is only where the cursor happens to be, which is the definition when the
 * user is on one and just a usage site otherwise.
 */
export interface DefinitionHint {
  readonly source: 'definition' | 'cursor' | 'none';
  /** Repository-relative path, forward slashes. Absent when unknown or outside the project. */
  readonly path?: string;
  /** 1-based line. */
  readonly line?: number;
  /** Set when go-to-definition leads outside the project, to a file the index cannot hold. */
  readonly outsideProject?: string;
}

export type Choice =
  /** The index has no definition under that name. */
  | { readonly kind: 'none' }
  /** One definition was chosen without asking. */
  | { readonly kind: 'chosen'; readonly definition: Definition; readonly basis: 'only-one' | 'matches-hint' }
  /**
   * The user must choose. 'ambiguous': several definitions share the name.
   * 'hint-mismatch': the language server points at a definition the index
   * does not have under this name, so every candidate may be a different
   * symbol that merely shares the name.
   */
  | { readonly kind: 'ask'; readonly candidates: readonly Definition[]; readonly reason: 'ambiguous' | 'hint-mismatch' };

/**
 * Chooses among the definitions the index holds under one name.
 *
 * A candidate in the hinted file wins; among several in that file, the one
 * at the hinted line, else the nearest one starting at or above it. When a
 * trusted hint matches no candidate, nothing is chosen silently, even if
 * there is only one candidate: it is probably a different symbol with the
 * same name. With only a cursor position to go on, a single candidate is
 * taken and several are put to the user.
 */
export function chooseDefinition(definitions: readonly Definition[], hint: DefinitionHint): Choice {
  if (definitions.length === 0) {
    return { kind: 'none' };
  }
  if (hint.path !== undefined) {
    const inFile = definitions.filter((d) => d.path === hint.path);
    if (inFile.length === 1) {
      return { kind: 'chosen', definition: inFile[0] as Definition, basis: 'matches-hint' };
    }
    if (inFile.length > 1) {
      if (hint.line !== undefined) {
        const line = hint.line;
        const exact = inFile.find((d) => d.line === line);
        if (exact !== undefined) {
          return { kind: 'chosen', definition: exact, basis: 'matches-hint' };
        }
        const above = inFile.filter((d) => d.line > 0 && d.line <= line).sort((a, b) => b.line - a.line)[0];
        if (above !== undefined) {
          return { kind: 'chosen', definition: above, basis: 'matches-hint' };
        }
      }
      return { kind: 'ask', candidates: inFile, reason: 'ambiguous' };
    }
  }
  if (hint.source === 'definition') {
    return { kind: 'ask', candidates: definitions, reason: 'hint-mismatch' };
  }
  if (definitions.length === 1) {
    return { kind: 'chosen', definition: definitions[0] as Definition, basis: 'only-one' };
  }
  return { kind: 'ask', candidates: definitions, reason: 'ambiguous' };
}

/**
 * The tool arguments that single out `chosen`: its name, its file and the
 * line it starts on. The line is left out when the index reported none, and
 * the tool then lists candidates if the file holds several.
 */
export function targetFor(chosen: Definition): QuestionTarget {
  const target: { name: string; path: string; line?: number } = { name: chosen.name, path: chosen.path };
  if (chosen.line > 0) {
    target.line = chosen.line;
  }
  return target;
}

/**
 * The definition a summary says it answered for.
 *
 * Every tool names it in its summary line, as
 * `... of parseToolArg (internal/cli/tool.go:384).` This reads that location
 * back. It returns undefined when the summary carries none, which is the
 * case for impact and tests from an older binary.
 */
export function answeredLocation(summary: string): { path: string; line: number } | undefined {
  const match = /\(([^()\s:][^()\s]*):(\d+)\)/u.exec(summary);
  if (match === null) {
    return undefined;
  }
  const line = Number.parseInt(match[2] as string, 10);
  if (!Number.isInteger(line) || line < 1) {
    return undefined;
  }
  return { path: match[1] as string, line };
}

/**
 * Warnings, written by the extension, about whether an answer is for the
 * definition the user chose. Empty when there is nothing to warn about.
 */
export function targetWarnings(chosen: Definition, all: readonly Definition[], answer: ToolAnswer): string[] {
  if (all.length <= 1) {
    return [];
  }
  const answered = answeredLocation(answer.summary);
  if (answered === undefined) {
    return [
      `${all.length} definitions are named "${chosen.name}" and the tool did not say which one it answered for. ` +
        `The one asked about is ${chosen.path}:${chosen.line}.`,
    ];
  }
  if (answered.path !== chosen.path || (chosen.line > 0 && answered.line !== chosen.line)) {
    return [
      `This answer is for the definition at ${answered.path}:${answered.line}, NOT the one you asked about ` +
        `(${chosen.path}:${chosen.line}). ${all.length} definitions are named "${chosen.name}" and the tool ` +
        'did not single yours out. Do not read this as an answer about your symbol.',
    ];
  }
  return [];
}

/** A one-line label for a definition in a pick list: `function  internal/cli/tool.go:384`. */
export function describeDefinition(def: Definition): string {
  const where = def.line > 0 ? `${def.path}:${def.line}` : def.path;
  return def.kind !== '' ? `${def.kind}  ${where}` : where;
}
