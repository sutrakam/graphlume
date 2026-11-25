// Builds the argument arrays the extension passes to the graphlume binary.
//
// Two rules hold for everything in this file:
//
//  1. The result is always an argv ARRAY for a process started without a
//     shell. Nothing here is ever joined into a command line.
//  2. Text that came from the editor travels only inside one JSON object,
//     given as `--args=<json>`. The CLI's `key=value` form is never used for
//     it, because that form turns `true`, `null` and numbers into other JSON
//     types and reads a FILE for a value starting with `@`.

import type { Confidence } from './confidence';

/** The tools this extension calls. Names are fixed; none comes from user input. */
export const TOOLS = [
  'symbol',
  'callers',
  'impact',
  'tests',
  'implementations',
  'overview',
] as const;

export type ToolName = (typeof TOOLS)[number];

/** The four questions a user can ask about the symbol under the cursor. */
export type QuestionKind = 'callers' | 'impact' | 'tests' | 'implementations';

export const QUESTION_TOOL: Readonly<Record<QuestionKind, ToolName>> = {
  callers: 'callers',
  impact: 'impact',
  tests: 'tests',
  implementations: 'implementations',
};

export const QUESTION_TITLE: Readonly<Record<QuestionKind, string>> = {
  callers: 'Callers of',
  impact: 'What could break if you change',
  tests: 'Tests that reach',
  implementations: 'Implementations of',
};

/** Longest symbol name sent to the tool. Real identifiers are far shorter. */
export const MAX_SYMBOL_LENGTH = 200;

export type SymbolNameCheck =
  | { readonly ok: true; readonly name: string }
  | { readonly ok: false; readonly reason: string };

/**
 * Checks text taken from the editor before it is used as a symbol name.
 *
 * This is not what makes the call safe: the JSON encoding and the shell-free
 * spawn do that. It exists to refuse input that cannot be an identifier, so
 * the user gets a clear message instead of a confusing "not found".
 */
export function checkSymbolName(raw: string): SymbolNameCheck {
  const name = raw.trim();
  if (name === '') {
    return { ok: false, reason: 'There is no symbol under the cursor.' };
  }
  if (name.length > MAX_SYMBOL_LENGTH) {
    return { ok: false, reason: `The selection is longer than ${MAX_SYMBOL_LENGTH} characters, so it is not a symbol name.` };
  }
  // Control characters (U+0000 to U+001F, U+007F) and any whitespace.
  if (/[\u0000-\u001f\u007f\s]/u.test(name)) {
    return { ok: false, reason: 'The selection contains whitespace or control characters, so it is not a symbol name.' };
  }
  if (!/[\p{L}\p{N}_$]/u.test(name)) {
    return { ok: false, reason: 'The selection contains no letters or digits, so it is not a symbol name.' };
  }
  return { ok: true, name };
}

/** One definition the question is about. */
export interface QuestionTarget {
  /** The bare symbol name. */
  readonly name: string;
  /** Repository-relative path of the definition, with forward slashes. */
  readonly path?: string;
  /** 1-based line the definition starts on, as the index reports it. Used only with `path`. */
  readonly line?: number;
}

/** The argument that carries the symbol name, which differs by tool. */
const NAME_ARGUMENT: Readonly<Record<QuestionKind, 'name' | 'symbol'>> = {
  callers: 'name',
  implementations: 'name',
  impact: 'symbol',
  tests: 'symbol',
};

/** A tool's arguments. `line` is a JSON number; everything else is a string. */
export type ToolArguments = Record<string, string | number>;

/**
 * The JSON arguments object for one question.
 *
 * Every tool takes the bare name (as `name` or `symbol`) and selects one
 * definition with `path` and `line`. The fully qualified name is not sent:
 * overloads in one file share it, so the tool treats it as ambiguous and
 * lists candidates instead of answering. min_confidence is always sent, so
 * the floor in force is the one the user chose and the tool reports what it
 * left out.
 */
export function questionArguments(kind: QuestionKind, target: QuestionTarget, minConfidence: Confidence): ToolArguments {
  const args: ToolArguments = { [NAME_ARGUMENT[kind]]: target.name };
  if (target.path !== undefined && target.path !== '') {
    args['path'] = target.path;
    // A line without its file would match a definition on that line anywhere.
    if (target.line !== undefined && Number.isInteger(target.line) && target.line > 0) {
      args['line'] = target.line;
    }
  }
  args['min_confidence'] = minConfidence;
  return args;
}

/** Arguments for the definition lookup that precedes every question. */
export function findSymbolArguments(name: string): Record<string, string> {
  return { name };
}

/**
 * argv for `graphlume --project=<root> --json tool call <tool> --args=<json>`.
 *
 * `--flag=value` is used rather than `--flag value` so a value can never be
 * read as another flag, whatever it starts with.
 */
export function toolCallArgv(projectRoot: string, tool: ToolName, args: Readonly<Record<string, unknown>>): string[] {
  if (!(TOOLS as readonly string[]).includes(tool)) {
    throw new Error(`unknown tool: ${String(tool)}`);
  }
  return [`--project=${projectRoot}`, '--json', 'tool', 'call', tool, `--args=${JSON.stringify(args)}`];
}

/** argv for `graphlume --project=<root> --json status`. */
export function statusArgv(projectRoot: string): string[] {
  return [`--project=${projectRoot}`, '--json', 'status'];
}

/** The subcommands that build an index, in the order they must run. */
export type IndexStep = 'init' | 'index' | 'resolve';

/** argv for one index-building step, run in a terminal the user can see. */
export function indexStepArgv(projectRoot: string, step: IndexStep): string[] {
  return [`--project=${projectRoot}`, step];
}

/**
 * Which steps to run for the state the index is in.
 *
 * `init` creates the database and is needed only when there is none. `index`
 * parses the files. `resolve` binds references to definitions; without it
 * every caller and impact answer is empty, so it always follows `index`.
 */
export function indexStepsFor(indexStatus: string | undefined): IndexStep[] {
  return indexStatus === undefined || indexStatus === 'absent' ? ['init', 'index', 'resolve'] : ['index', 'resolve'];
}

/**
 * Expands a leading `~` in the configured binary path. Nothing else is
 * interpreted: no variables, no globbing.
 */
export function expandBinaryPath(configured: string, homeDir: string): string {
  const value = configured.trim();
  if (value === '') {
    return 'graphlume';
  }
  if (value === '~') {
    return homeDir;
  }
  if (value.startsWith('~/') || value.startsWith('~\\')) {
    return homeDir.replace(/[\\/]+$/u, '') + value.slice(1);
  }
  return value;
}
