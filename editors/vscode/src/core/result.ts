// Parses what `graphlume --json tool call` prints, and decides what one run
// of the binary means.
//
// The envelope is `{tool, text, result, is_error}`. `result` is the tool's
// structured answer and is null when the arguments failed validation. Every
// field is read defensively: an older or newer binary may omit or add fields,
// and a wrong type is treated as an absent value rather than trusted.

import { confidenceRank } from './confidence';

export interface ToolItem {
  readonly kind: string;
  readonly name?: string;
  readonly fqn?: string;
  readonly path?: string;
  /** 1-based. Absent when the tool reported no line. */
  readonly line?: number;
  readonly confidence?: string;
  readonly source?: string;
  readonly why?: string;
}

/**
 * `outcome` on an answer that lists definitions to choose from. The name
 * matched several, the tool analysed nothing, and every item is a candidate.
 */
export const AMBIGUOUS_NAME = 'ambiguous_name';

export interface Provenance {
  readonly builtAtSha?: string;
  readonly headSha?: string;
  /** Compares commits only. Says nothing about uncommitted changes. */
  readonly headMatchesBuild: boolean;
  readonly ageSeconds: number;
  /** Always set by the tool: what the comparison does not cover. */
  readonly caveat: string;
}

export interface Verdict {
  readonly status: string;
  readonly counts: Readonly<Record<string, number>>;
  readonly signals: readonly string[];
  readonly nextStep: string;
}

export interface CountBy {
  readonly label: string;
  readonly count: number;
}

/** Results the tool removed to fit its response budget. */
export interface Omission {
  readonly count: number;
  readonly byKind: readonly CountBy[];
  readonly byConfidence: readonly CountBy[];
  readonly weakestShown?: string;
  readonly strongerDropped: readonly string[];
}

/** How far the index supports an answer, as the tool judged it. */
export interface Reliability {
  /** good, incomplete, unverified, low or none. A level this extension does not know is kept as written. */
  readonly level: string;
  /** The clause the summary opens with when the level is not good. */
  readonly lead: string;
  /** The same finding at full length. The tool repeats these among its notes. */
  readonly reasons: readonly string[];
}

/** Which slice of the whole result an answer carries. */
export interface Page {
  readonly offset: number;
  readonly limit: number;
  /** Rows that exist. The tool counts them; it does not infer them from a full page. */
  readonly total: number;
  /** Rows this answer carries. */
  readonly returned: number;
  /** The offset that continues after these rows. 0 when the answer reaches the end. */
  readonly nextOffset: number;
}

export interface ToolAnswer {
  readonly tool: string;
  readonly isError: boolean;
  /** The text an MCP client receives, verbatim. */
  readonly text: string;
  /** False when `result` was null or not an object. */
  readonly hasResult: boolean;
  readonly summary: string;
  readonly indexRevision: number;
  readonly indexStatus: string;
  readonly items: readonly ToolItem[];
  readonly notes: readonly string[];
  readonly truncated: boolean;
  readonly truncatedReason?: string;
  readonly provenance?: Provenance;
  readonly verdict?: Verdict;
  readonly omitted?: Omission;
  /** Absent on an answer from a binary that does not grade its answers against coverage. */
  readonly reliability?: Reliability;
  /** Absent on a tool that does not page, and on an answer from an older binary. */
  readonly page?: Page;
  /** Set when the answer is not an answer to the question asked. See AMBIGUOUS_NAME. */
  readonly outcome?: string;
}

export class EnvelopeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'EnvelopeError';
  }
}

type Json = Record<string, unknown>;

function isObject(value: unknown): value is Json {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function str(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

function num(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string' && v !== '') : [];
}

function parseItem(raw: unknown): ToolItem | undefined {
  if (!isObject(raw)) {
    return undefined;
  }
  const line = num(raw['line']);
  const item: {
    kind: string;
    name?: string;
    fqn?: string;
    path?: string;
    line?: number;
    confidence?: string;
    source?: string;
    why?: string;
  } = { kind: str(raw['kind']) ?? '' };
  const name = str(raw['name']);
  const fqn = str(raw['fqn']);
  const path = str(raw['path']);
  const confidence = str(raw['confidence']);
  const source = str(raw['source']);
  const why = str(raw['why']);
  if (name !== undefined) item.name = name;
  if (fqn !== undefined) item.fqn = fqn;
  if (path !== undefined) item.path = path;
  if (line !== undefined && Number.isInteger(line) && line > 0) item.line = line;
  if (confidence !== undefined) item.confidence = confidence;
  if (source !== undefined) item.source = source;
  if (why !== undefined) item.why = why;
  return item;
}

function parseProvenance(raw: unknown): Provenance | undefined {
  if (!isObject(raw)) {
    return undefined;
  }
  const out: { builtAtSha?: string; headSha?: string; headMatchesBuild: boolean; ageSeconds: number; caveat: string } = {
    // Anything but a literal true is "not known to match".
    headMatchesBuild: raw['head_matches_build'] === true,
    ageSeconds: num(raw['age_seconds']) ?? 0,
    caveat: str(raw['caveat']) ?? '',
  };
  const built = str(raw['built_at_sha']);
  const head = str(raw['head_sha']);
  if (built !== undefined) out.builtAtSha = built;
  if (head !== undefined) out.headSha = head;
  return out;
}

function parseVerdict(raw: unknown): Verdict | undefined {
  if (!isObject(raw)) {
    return undefined;
  }
  const counts: Record<string, number> = {};
  if (isObject(raw['counts'])) {
    for (const [tier, count] of Object.entries(raw['counts'])) {
      const n = num(count);
      if (n !== undefined) {
        counts[tier] = n;
      }
    }
  }
  return {
    status: str(raw['status']) ?? '',
    counts,
    signals: strings(raw['signals']),
    nextStep: str(raw['next_step']) ?? '',
  };
}

function parseCounts(raw: unknown, labelKey: string): CountBy[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: CountBy[] = [];
  for (const entry of raw) {
    if (!isObject(entry)) continue;
    const label = str(entry[labelKey]);
    const count = num(entry['count']);
    if (label !== undefined && count !== undefined) {
      out.push({ label, count });
    }
  }
  return out;
}

function parseOmission(raw: unknown): Omission | undefined {
  if (!isObject(raw)) {
    return undefined;
  }
  const count = num(raw['count']) ?? 0;
  if (count <= 0) {
    return undefined;
  }
  const out: {
    count: number;
    byKind: CountBy[];
    byConfidence: CountBy[];
    weakestShown?: string;
    strongerDropped: string[];
  } = {
    count,
    byKind: parseCounts(raw['by_kind'], 'kind'),
    byConfidence: parseCounts(raw['by_confidence'], 'confidence'),
    strongerDropped: strings(raw['stronger_dropped']),
  };
  const weakest = str(raw['weakest_shown']);
  if (weakest !== undefined) out.weakestShown = weakest;
  return out;
}

function count(value: unknown): number | undefined {
  const n = num(value);
  return n !== undefined && Number.isInteger(n) && n >= 0 ? n : undefined;
}

function parseReliability(raw: unknown): Reliability | undefined {
  if (!isObject(raw)) {
    return undefined;
  }
  // Without a level there is no judgement to report.
  const level = str(raw['level']);
  if (level === undefined) {
    return undefined;
  }
  return { level, lead: str(raw['lead']) ?? '', reasons: strings(raw['reasons']) };
}

function parsePage(raw: unknown, rows: number): Page | undefined {
  if (!isObject(raw)) {
    return undefined;
  }
  // The total is the reason to read a page at all. Without a counted one the
  // answer is treated as unpaged, and its rows are counted as before.
  const total = count(raw['total']);
  if (total === undefined) {
    return undefined;
  }
  return {
    offset: count(raw['offset']) ?? 0,
    limit: count(raw['limit']) ?? 0,
    total,
    returned: count(raw['returned']) ?? rows,
    nextOffset: count(raw['next_offset']) ?? 0,
  };
}

/** Parses the JSON envelope printed by `graphlume --json tool call`. */
export function parseToolEnvelope(stdout: string): ToolAnswer {
  let raw: unknown;
  try {
    raw = JSON.parse(stdout);
  } catch (err) {
    throw new EnvelopeError(`graphlume did not print JSON: ${err instanceof Error ? err.message : String(err)}`);
  }
  if (!isObject(raw) || typeof raw['is_error'] !== 'boolean') {
    throw new EnvelopeError('graphlume printed JSON that is not a tool result (no is_error field).');
  }
  const result = isObject(raw['result']) ? raw['result'] : undefined;
  const items: ToolItem[] = [];
  if (result !== undefined && Array.isArray(result['items'])) {
    for (const entry of result['items']) {
      const item = parseItem(entry);
      if (item !== undefined) {
        items.push(item);
      }
    }
  }
  const answer: {
    tool: string;
    isError: boolean;
    text: string;
    hasResult: boolean;
    summary: string;
    indexRevision: number;
    indexStatus: string;
    items: ToolItem[];
    notes: string[];
    truncated: boolean;
    truncatedReason?: string;
    provenance?: Provenance;
    verdict?: Verdict;
    omitted?: Omission;
    reliability?: Reliability;
    page?: Page;
    outcome?: string;
  } = {
    tool: str(raw['tool']) ?? '',
    isError: raw['is_error'],
    text: typeof raw['text'] === 'string' ? raw['text'] : '',
    hasResult: result !== undefined,
    summary: str(result?.['summary']) ?? '',
    indexRevision: num(result?.['index_revision']) ?? 0,
    indexStatus: str(result?.['index_status']) ?? '',
    items,
    notes: strings(result?.['notes']),
    truncated: result?.['truncated'] === true,
  };
  const reason = str(result?.['truncated_reason']);
  const provenance = parseProvenance(result?.['provenance']);
  const verdict = parseVerdict(result?.['verdict']);
  const omitted = parseOmission(result?.['omitted']);
  if (reason !== undefined) answer.truncatedReason = reason;
  if (provenance !== undefined) answer.provenance = provenance;
  if (verdict !== undefined) answer.verdict = verdict;
  const reliability = parseReliability(result?.['reliability']);
  const page = parsePage(result?.['page'], items.length);
  const outcome = str(result?.['outcome']);
  if (omitted !== undefined) answer.omitted = omitted;
  if (reliability !== undefined) answer.reliability = reliability;
  if (page !== undefined) answer.page = page;
  if (outcome !== undefined) answer.outcome = outcome;
  return answer;
}

/**
 * The readable part of a failed tool's text.
 *
 * A failure reads `=== SYSTEM METADATA ===\nerror_code: internal\n<message>\nhint: ...`
 * or, for an argument the schema rejected, one bare line. The section header
 * and the error_code line are dropped; the tool's own message and hint stay.
 */
export function failureMessage(answer: ToolAnswer): string {
  const lines = answer.text
    .split(/\r?\n/u)
    .map((line) => line.trim())
    .filter((line) => line !== '' && !/^===.*===$/u.test(line) && !/^error_code:/u.test(line));
  const message = lines.join(' ').trim();
  return message !== '' ? message : 'The tool reported a failure and gave no message.';
}

/** What the extension should make of one run of `graphlume tool call`. */
export type ToolOutcome =
  /** The tool answered. This includes "nothing found". */
  | { readonly kind: 'answer'; readonly answer: ToolAnswer }
  /** There is no index to answer from. */
  | { readonly kind: 'no-index'; readonly message: string; readonly answer?: ToolAnswer }
  /** The tool ran and set its failure flag (the CLI exits 1). */
  | { readonly kind: 'tool-failure'; readonly message: string; readonly answer: ToolAnswer }
  /** The CLI itself failed before or instead of the tool: bad flag, unknown tool, unreadable index. */
  | { readonly kind: 'cli-error'; readonly message: string; readonly exitCode: number | null };

function stripErrorPrefix(stderr: string): string {
  return stderr
    .split(/\r?\n/u)
    .map((line) => line.trim())
    .filter((line) => line !== '')
    .map((line) => line.replace(/^error:\s*/iu, ''))
    .join(' ');
}

/**
 * Decides what a finished `tool call` process means.
 *
 * The CLI exits 1 both when a tool reports failure (JSON on stdout, is_error
 * true) and when the CLI itself fails (nothing on stdout, `error: ...` on
 * stderr), so stdout is read first and the exit status only breaks ties.
 */
export function interpretToolRun(run: { readonly code: number | null; readonly stdout: string; readonly stderr: string }): ToolOutcome {
  if (run.stdout.trim() !== '') {
    let answer: ToolAnswer;
    try {
      answer = parseToolEnvelope(run.stdout);
    } catch (err) {
      return { kind: 'cli-error', message: err instanceof Error ? err.message : String(err), exitCode: run.code };
    }
    if (answer.isError) {
      return { kind: 'tool-failure', message: failureMessage(answer), answer };
    }
    if (answer.indexStatus === 'absent') {
      // A success-shaped answer saying the project was initialised but never indexed.
      const message = [answer.summary, ...answer.notes].filter((s) => s !== '').join(' ');
      return { kind: 'no-index', message: message !== '' ? message : 'No index has been built for this project yet.', answer };
    }
    return { kind: 'answer', answer };
  }
  const stderr = stripErrorPrefix(run.stderr);
  if (/\bno index at\b/iu.test(stderr)) {
    return { kind: 'no-index', message: stderr };
  }
  if (stderr !== '') {
    return { kind: 'cli-error', message: stderr, exitCode: run.code };
  }
  return {
    kind: 'cli-error',
    message: `graphlume exited with status ${run.code === null ? 'unknown' : String(run.code)} and printed nothing.`,
    exitCode: run.code,
  };
}

/**
 * True when the answer lists definitions to choose from. Nothing was
 * analysed, so its items must never be shown as callers, tests or impact.
 */
export function isAmbiguous(answer: ToolAnswer): boolean {
  return answer.outcome === AMBIGUOUS_NAME;
}

/**
 * How many rows the tool counted but did not return. 0 when it returned them
 * all, and when it gave no count: an older binary says only `truncated`.
 */
export function rowsWithheld(answer: ToolAnswer): number {
  const page = answer.page;
  if (page === undefined) {
    return 0;
  }
  const beyond = page.total - page.offset - answer.items.length;
  // next_offset alone says rows follow, even if the arithmetic does not.
  return beyond > 0 ? beyond : page.nextOffset > 0 ? 1 : 0;
}

/** Why an answer must not be taken at face value. */
export interface Doubt {
  /** One or two words for a title: unknown, unreliable, unverified, incomplete, mixed confidence. */
  readonly word: string;
  /**
   * 'reliability' when the index's coverage is the reason. 'verdict' when
   * only the rows are: their tiers differ, or some were cut.
   */
  readonly from: 'reliability' | 'verdict';
}

// The word the tool itself opens the summary with for each level. Maps, so
// that a level or status named like an object method is not found by accident.
const RELIABILITY_WORDS: ReadonlyMap<string, string> = new Map([
  ['none', 'unknown'],
  ['low', 'unreliable'],
  ['unverified', 'unverified'],
  ['incomplete', 'incomplete'],
]);

const VERDICT_WORDS: ReadonlyMap<string, string> = new Map([
  ['unreliable', 'unreliable'],
  ['incomplete', 'incomplete'],
  ['mixed', 'mixed confidence'],
]);

/**
 * Whether the tool said this answer is doubtful, and in which word.
 *
 * Any reliability level other than `good` counts, including one this
 * extension has never seen: an unknown level is not a good one. Without a
 * reliability the verdict decides. Undefined means the tool raised no doubt,
 * which is still a lower bound and never proof.
 */
export function doubtOf(answer: ToolAnswer): Doubt | undefined {
  const level = answer.reliability?.level;
  if (level !== undefined && level !== 'good') {
    return { word: RELIABILITY_WORDS.get(level) ?? level, from: 'reliability' };
  }
  const word = VERDICT_WORDS.get(answer.verdict?.status ?? '');
  return word !== undefined ? { word, from: 'verdict' } : undefined;
}

/** True for a verdict that says the rows are not to be acted on as they stand. */
export function verdictIsWarning(status: string): boolean {
  return status === 'weak' || status === 'empty' || VERDICT_WORDS.has(status);
}

/** One statement, in the tool's own words, about what an answer does not cover. */
export interface LimitStatement {
  readonly kind: 'reliability' | 'stale' | 'truncated' | 'omitted' | 'note' | 'signal' | 'caveat';
  /** A short label for the row. */
  readonly label: string;
  readonly text: string;
  /** True for the statements a reader must not miss. */
  readonly warning: boolean;
}

function describeOmission(o: Omission): string {
  const parts = [`${o.count} result(s) were left out to fit the response`];
  if (o.byKind.length > 0) {
    parts.push(`by kind: ${o.byKind.map((c) => `${c.label} ${c.count}`).join(', ')}`);
  }
  if (o.byConfidence.length > 0) {
    parts.push(`by confidence: ${o.byConfidence.map((c) => `${c.label} ${c.count}`).join(', ')}`);
  }
  if (o.weakestShown !== undefined) {
    parts.push(`weakest tier shown: ${o.weakestShown}`);
  }
  if (o.strongerDropped.length > 0) {
    parts.push(`STRONGER evidence than what is shown was left out: ${o.strongerDropped.join(', ')}`);
  }
  return parts.join('; ') + '.';
}

/**
 * Everything the tool said about the limits of its own answer, in the order
 * a reader needs it. Nothing is dropped or summarised away: each note is
 * passed through verbatim. A sentence the tool gave twice, as the reliability
 * lead and again as a verdict signal or a note, is shown once.
 */
export function limitsOf(answer: ToolAnswer): LimitStatement[] {
  const out: LimitStatement[] = [];
  const reliability = answer.reliability !== undefined && answer.reliability.level !== 'good' ? answer.reliability : undefined;
  if (reliability !== undefined) {
    // First, because it decides how everything below it is to be read.
    out.push({
      kind: 'reliability',
      label: 'Reliability',
      text: reliability.lead !== '' ? `${reliability.level} — ${reliability.lead}` : reliability.level,
      warning: true,
    });
    for (const reason of reliability.reasons) {
      if (!answer.notes.includes(reason)) {
        out.push({ kind: 'reliability', label: 'Reliability', text: reason, warning: true });
      }
    }
  }
  const prov = answer.provenance;
  const stale = prov !== undefined && !prov.headMatchesBuild && answer.indexStatus !== 'absent';
  if (stale && prov.caveat !== '') {
    out.push({ kind: 'stale', label: 'Index', text: prov.caveat, warning: true });
  }
  if (answer.truncated) {
    out.push({
      kind: 'truncated',
      label: 'Truncated',
      text: answer.truncatedReason ?? 'The answer was cut short; the tool gave no reason.',
      warning: true,
    });
  }
  if (answer.omitted !== undefined) {
    out.push({
      kind: 'omitted',
      label: 'Left out',
      text: describeOmission(answer.omitted),
      warning: answer.omitted.strongerDropped.length > 0,
    });
  }
  for (const note of answer.notes) {
    out.push({
      kind: 'note',
      label: 'Note',
      text: note,
      warning:
        /NOT VISIBLE|LOWER BOUND|unresolved|incomplete|omitted|not proof/iu.test(note) ||
        (reliability !== undefined && reliability.reasons.includes(note)),
    });
  }
  for (const signal of answer.verdict?.signals ?? []) {
    if (reliability !== undefined && signal === reliability.lead) {
      continue;
    }
    out.push({ kind: 'signal', label: 'Signal', text: signal, warning: true });
  }
  if (prov !== undefined && prov.caveat !== '' && !stale) {
    out.push({ kind: 'caveat', label: 'Index', text: prov.caveat, warning: false });
  }
  return out;
}

/** "strong (high 3)" and the like, from the tool's verdict. */
export function verdictLine(verdict: Verdict): string {
  const counts = Object.entries(verdict.counts)
    .filter(([, n]) => n > 0)
    .sort(([a], [b]) => confidenceRank(a) - confidenceRank(b))
    .map(([tier, n]) => `${tier} ${n}`)
    .join(', ');
  return counts !== '' ? `${verdict.status} (${counts})` : verdict.status;
}
