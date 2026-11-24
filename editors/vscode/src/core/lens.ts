// The pure half of the CodeLens: which definitions get a lens, what name to
// ask GraphLume about, and what a callers answer says about one of them.

import type { ToolAnswer } from './result';
import { doubtOf, isAmbiguous, rowsWithheld } from './result';
import { answeredLocation } from './target';

/** Most lenses placed in one file. Definitions past this get none. */
export const MAX_LENSES_PER_FILE = 60;

/** A definition as the editor's outline reports it. */
export interface OutlineSymbol {
  /** The outline's display name, which may carry a receiver or a parameter list. */
  readonly name: string;
  /** 'function' for functions, methods and constructors; 'type' for classes, interfaces, structs and enums. */
  readonly category: 'function' | 'type' | 'other';
  /** 0-based line of the name. */
  readonly line: number;
  readonly children?: readonly OutlineSymbol[];
}

export interface LensTarget {
  /** The bare name to ask GraphLume about. */
  readonly name: string;
  /** 0-based line the lens sits on. */
  readonly line: number;
  readonly category: 'function' | 'type';
  /** True when another lens in the same file asks about the same name. */
  readonly sharedName: boolean;
}

/**
 * The bare identifier inside an outline name, or undefined when there is
 * none worth asking about.
 *
 * Language servers decorate names: gopls writes `(*Server).findCallers`,
 * Java writes `handle(Request, int)`, C++ writes `Parser::parse`, and
 * TypeScript writes `<function>` or `describe('x') callback` for anonymous
 * code. The index knows the bare name only.
 */
export function bareSymbolName(outlineName: string): string | undefined {
  let name = outlineName.trim();
  // Go receiver: "(*Server).findCallers" or "(Server).String".
  const receiver = /^\([^)]*\)\.(.+)$/u.exec(name);
  if (receiver !== null) {
    name = receiver[1] as string;
  }
  // Parameter list or generic parameters: "handle(Request, int)", "Box<T>".
  // The brackets must close the name: "describe('x') callback" is not a
  // symbol called describe.
  const head = /^([^(<[\s]+)\s*(?:[(<[].*[)>\]])?$/u.exec(name);
  if (head === null) {
    return undefined;
  }
  name = head[1] as string;
  // Qualified: "Parser::parse", "Outer.inner".
  const last = name.split(/::|\.|#/u).pop();
  name = (last ?? '').trim();
  if (!/^[\p{L}_$][\p{L}\p{N}_$]*$/u.test(name)) {
    return undefined;
  }
  return name;
}

/**
 * The definitions that get a lens: functions and types, at any nesting
 * depth, in document order, capped at `max`.
 */
export function lensTargets(symbols: readonly OutlineSymbol[], max: number = MAX_LENSES_PER_FILE): LensTarget[] {
  const found: { name: string; line: number; category: 'function' | 'type' }[] = [];
  const walk = (nodes: readonly OutlineSymbol[], depth: number): void => {
    for (const node of nodes) {
      if (node.category !== 'other') {
        const name = bareSymbolName(node.name);
        if (name !== undefined) {
          found.push({ name, line: node.line, category: node.category });
        }
      }
      // Functions nested in functions are local helpers and closures; the
      // index rarely has them, so only types are searched for members.
      if (node.children !== undefined && depth < 8 && node.category !== 'function') {
        walk(node.children, depth + 1);
      }
    }
  };
  walk(symbols, 0);
  found.sort((a, b) => a.line - b.line);

  // One lens per line: a second symbol on the same line would stack lenses.
  const byLine = new Map<number, { name: string; line: number; category: 'function' | 'type' }>();
  for (const f of found) {
    if (!byLine.has(f.line)) {
      byLine.set(f.line, f);
    }
  }
  const unique = [...byLine.values()].slice(0, Math.max(0, max));
  const counts = new Map<string, number>();
  for (const u of unique) {
    counts.set(u.name, (counts.get(u.name) ?? 0) + 1);
  }
  return unique.map((u) => ({ ...u, sharedName: (counts.get(u.name) ?? 0) > 1 }));
}

/** What a callers answer says about the definition a lens sits on. */
export type LensCount =
  /** The tool answered for a definition in this file. */
  | {
      readonly kind: 'count';
      /** Distinct calling symbols among the rows returned. */
      readonly callers: number;
      /** Call sites: the tool's own count when it gave one, else the rows returned. */
      readonly sites: number;
      /** True when the tool cut the list and counted nothing, so both numbers are floors. */
      readonly atLeast: boolean;
      /**
       * Rows returned, set only when the tool counted more call sites than it
       * returned. `callers` then covers this part of the list alone.
       */
      readonly shown?: number;
      /** The tool's word for a count that is not to be trusted: `unreliable`, `unverified`. */
      readonly doubt?: string;
    }
  /** The tool answered for a same-named definition in another file. */
  | { readonly kind: 'other-definition'; readonly path: string; readonly line: number }
  /** The name matched several definitions and the tool answered for none of them. */
  | { readonly kind: 'ambiguous' }
  /** The tool said nothing can be concluded about callers. Its count is not a count. */
  | { readonly kind: 'unknown' }
  /** The index has no definition under this name. */
  | { readonly kind: 'not-indexed' };

/**
 * Reads a callers answer for a lens in `filePath` (repository-relative).
 *
 * A list of candidates is checked for first: its rows are definitions, and
 * counting them would show the number of namesakes as a number of callers.
 *
 * The check on the answered location matters for an older binary, whose path
 * hint is a preference, not a filter: for a name shared by many definitions
 * it can answer for one in a different file. A count for another function
 * must not be shown above this one.
 */
export function lensCount(answer: ToolAnswer, filePath: string): LensCount {
  if (isAmbiguous(answer)) {
    return { kind: 'ambiguous' };
  }
  const answered = answeredLocation(answer.summary);
  if (answered === undefined) {
    return { kind: 'not-indexed' };
  }
  if (answered.path !== filePath) {
    return { kind: 'other-definition', path: answered.path, line: answered.line };
  }
  if (answer.reliability?.level === 'none') {
    return { kind: 'unknown' };
  }
  const distinct = new Set<string>();
  for (const item of answer.items) {
    distinct.add(`${item.path ?? ''}\u0000${item.name ?? ''}`);
  }
  const rows = answer.items.length;
  const counted = answer.page !== undefined;
  const count: { kind: 'count'; callers: number; sites: number; atLeast: boolean; shown?: number; doubt?: string } = {
    kind: 'count',
    callers: distinct.size,
    sites: counted ? Math.max(answer.page.total, rows) : rows,
    // With a counted total the number is exact, however many rows came back.
    atLeast: answer.truncated && !counted,
  };
  if (rowsWithheld(answer) > 0) {
    count.shown = rows;
  }
  const doubt = doubtOf(answer);
  // A verdict of "incomplete" on a counted list says rows were cut. The
  // count shown is the tool's total, which the cut does not touch.
  if (doubt !== undefined && !(doubt.from === 'verdict' && doubt.word === 'incomplete' && counted)) {
    count.doubt = doubt.word;
  }
  return count;
}

function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

/** The lens text. Zero is worded as "found", never as "unused". */
export function lensTitle(count: LensCount, target: Pick<LensTarget, 'sharedName'>): string {
  switch (count.kind) {
    case 'not-indexed':
      return 'GraphLume: not in the index';
    case 'other-definition':
      return 'GraphLume: callers unknown (name is shared)';
    case 'ambiguous':
      return 'GraphLume: callers unknown (name matches several definitions)';
    case 'unknown':
      return 'GraphLume: callers unknown';
    case 'count': {
      const plus = count.atLeast ? '+' : '';
      let title: string;
      if (count.sites === 0) {
        title = 'GraphLume: 0 callers found';
      } else if (count.shown !== undefined) {
        // Distinct callers cannot be counted from part of the list, so only
        // the tool's own total is stated.
        title = `GraphLume: ${count.sites} ${plural(count.sites, 'call site', 'call sites')}`;
      } else {
        title = `GraphLume: ${count.callers}${plus} ${plural(count.callers, 'caller', 'callers')}`;
        if (count.sites !== count.callers) {
          title += `, ${count.sites}${plus} call sites`;
        }
      }
      if (count.doubt !== undefined) {
        title += ` (${count.doubt})`;
      }
      return target.sharedName ? `${title} (name shared in this file)` : title;
    }
  }
}

/** The lens tooltip: the tool's summary and every note it attached. */
export function lensTooltip(count: LensCount, answer: ToolAnswer, minConfidence: string): string {
  const lines: string[] = [];
  if (count.kind === 'other-definition') {
    lines.push(
      `GraphLume answered for the definition at ${count.path}:${count.line}, not this one. ` +
        'Several definitions share this name and its path hint could not single this one out.',
    );
  }
  if (answer.summary !== '') {
    lines.push(answer.summary);
  }
  if (count.kind === 'ambiguous') {
    // The notes of a candidate list tell an agent how to call the tool again.
    // Here the click does that.
    lines.push('Nothing was counted. Click to choose a definition and list its callers.');
    return lines.join('\n\n');
  }
  if (count.kind === 'count' && count.shown !== undefined) {
    lines.push(`The number is the tool's count of call sites. It returned ${count.shown} of them.`);
  }
  if (answer.truncated && answer.truncatedReason !== undefined) {
    lines.push(`Truncated: ${answer.truncatedReason}`);
  }
  lines.push(...answer.notes);
  if (minConfidence !== 'low') {
    lines.push(`Counting results at confidence "${minConfidence}" and above (graphlume.minConfidence).`);
  }
  lines.push('Click to list the callers.');
  return lines.join('\n\n');
}

/**
 * Cache key for one lens lookup. Changes whenever the index is rebuilt.
 *
 * `line` is given for a name the file defines more than once: each of those
 * lenses asks about its own definition, so each needs its own entry.
 */
export function lensCacheKey(
  projectRoot: string,
  token: string,
  minConfidence: string,
  filePath: string,
  name: string,
  line?: number,
): string {
  const parts = [projectRoot, token, minConfidence, filePath, name];
  if (line !== undefined) {
    parts.push(String(line));
  }
  return parts.join('\u0000');
}
