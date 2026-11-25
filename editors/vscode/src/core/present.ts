// Turns a tool answer into the rows the results view shows.
//
// The view has two jobs that pull against each other: be quick to scan, and
// never hide what GraphLume said it could not see. So the limits come first,
// in their own section, in the tool's own words; every result row carries
// its confidence tier; and nothing the tool returned is filtered out here.

import type { QuestionKind } from './args';
import { QUESTION_TITLE } from './args';
import { describeConfidence } from './confidence';
import type { LimitStatement, ToolAnswer, ToolItem } from './result';
import { doubtOf, limitsOf, rowsWithheld, verdictIsWarning, verdictLine } from './result';

export interface LimitRow {
  readonly type: 'limit';
  readonly label: string;
  readonly text: string;
  readonly warning: boolean;
  /** 'extension' for a note this extension wrote; 'tool' for GraphLume's own words. */
  readonly author: 'tool' | 'extension';
}

export interface ResultRow {
  readonly type: 'result';
  /** The symbol name, or the path when the tool gave no name. */
  readonly label: string;
  /** Always states the tier: `high · internal/cli/tool.go:323`. */
  readonly description: string;
  /** exact, high, medium, low, or '' when the tool reported none. */
  readonly confidence: string;
  readonly kind: string;
  /** Repository-relative path as the tool reported it. */
  readonly path?: string;
  /** 1-based. Absent when the tool reported no line. */
  readonly line?: number;
  readonly name?: string;
  readonly tooltip: string;
}

export interface Section {
  readonly title: string;
  readonly rows: readonly (LimitRow | ResultRow)[];
  readonly warning: boolean;
}

export interface ResultModel {
  /** View title: `Callers of parseToolArg`. */
  readonly title: string;
  /** The tool's own one-line summary. */
  readonly summary: string;
  readonly sections: readonly Section[];
  /** Rows in the view. */
  readonly resultCount: number;
  /** Rows that exist, as the tool counted them. Equal to resultCount when it gave no count. */
  readonly totalCount: number;
  /**
   * The view's one-line status: `3 rows`, `showing 50 of 63`, and ahead of
   * that the tool's word for a doubtful answer: `unreliable · 1 row`.
   */
  readonly status: string;
}

const KIND_TITLES: Readonly<Record<string, string>> = {
  calls: 'Call sites',
  direct: 'Direct: proven call edge to this symbol',
  indirect: 'Indirect: reaches this symbol through other calls',
  test: 'Tests',
  subtype: 'Implements or extends this',
  supertype: 'This implements or extends',
  // The extension asks the user to choose before it shows anything, so these
  // rows should not reach the view. If they do, the heading says what they are.
  candidate: 'Definitions that share the name: nothing was analysed',
};

function kindTitle(kind: string): string {
  return KIND_TITLES[kind] ?? (kind !== '' ? kind : 'Results');
}

function rowFor(item: ToolItem): ResultRow {
  const tier = item.confidence ?? '';
  const where = item.path !== undefined ? (item.line !== undefined ? `${item.path}:${item.line}` : item.path) : '';
  const tooltip: string[] = [];
  tooltip.push(describeConfidence(item.confidence));
  if (item.why !== undefined) tooltip.push(`Why: ${item.why}`);
  if (item.source !== undefined) tooltip.push(`Source: ${item.source}`);
  if (where !== '') tooltip.push(where);
  if (item.path !== undefined && item.line === undefined) {
    tooltip.push('GraphLume reported no line for this row. Opening it searches the file for the name.');
  }
  const row: {
    type: 'result';
    label: string;
    description: string;
    confidence: string;
    kind: string;
    path?: string;
    line?: number;
    name?: string;
    tooltip: string;
  } = {
    type: 'result',
    label: item.name ?? item.path ?? '(unnamed)',
    description: [tier !== '' ? tier : 'no tier', where].filter((s) => s !== '').join(' · '),
    confidence: tier,
    kind: item.kind,
    tooltip: tooltip.join('\n'),
  };
  if (item.path !== undefined) row.path = item.path;
  if (item.line !== undefined) row.line = item.line;
  if (item.name !== undefined) row.name = item.name;
  return row;
}

/**
 * One limit as a single line: `Truncated: depth limit 3 reached`. A note the
 * tool already began with the same word is not labelled twice.
 */
export function limitLine(row: Pick<LimitRow, 'label' | 'text'>): string {
  const text = row.text.trim();
  return text.toLowerCase().startsWith(row.label.toLowerCase() + ':') ? text : `${row.label}: ${text}`;
}

function limitRow(limit: LimitStatement): LimitRow {
  return { type: 'limit', label: limit.label, text: limit.text, warning: limit.warning, author: 'tool' };
}

export interface ModelOptions {
  readonly kind: QuestionKind;
  readonly symbol: string;
  /** Notes written by the extension, such as a possible wrong-definition answer. */
  readonly extensionWarnings?: readonly string[];
  /** The definition asked about, for the header: `internal/cli/tool.go:384`. */
  readonly askedAbout?: string;
}

/** Builds the rows for one answer. Rows keep the order the tool returned within each group. */
export function buildResultModel(answer: ToolAnswer, options: ModelOptions): ResultModel {
  const sections: Section[] = [];

  const limits: LimitRow[] = [];
  for (const warning of options.extensionWarnings ?? []) {
    limits.push({ type: 'limit', label: 'Check', text: warning, warning: true, author: 'extension' });
  }
  for (const limit of limitsOf(answer)) {
    limits.push(limitRow(limit));
  }
  if (answer.verdict !== undefined) {
    limits.push({
      type: 'limit',
      label: 'Verdict',
      text: [verdictLine(answer.verdict), answer.verdict.nextStep].filter((s) => s !== '').join('. '),
      warning: verdictIsWarning(answer.verdict.status),
      author: 'tool',
    });
  }
  if (rowsWithheld(answer) > 0) {
    // The tool's own advice is to pass an offset, which this view cannot do.
    const shown = answer.items.length;
    const total = totalRows(answer);
    limits.push({
      type: 'limit',
      label: 'Shown',
      text:
        (total > shown
          ? `This view holds ${shown} of the ${total} rows GraphLume counted. `
          : `This view holds ${shown} rows and GraphLume says more exist. `) +
        'The extension reads the first page only; to read on, run the same tool call in a terminal with the offset the tool names.',
      warning: true,
      author: 'extension',
    });
  }
  if (limits.length > 0) {
    sections.push({
      title: `What GraphLume says about this answer (${limits.length})`,
      rows: limits,
      warning: limits.some((l) => l.warning),
    });
  }

  // Group by kind, keeping first-seen order, so "direct" stays ahead of "indirect".
  const groups = new Map<string, ResultRow[]>();
  for (const item of answer.items) {
    const rows = groups.get(item.kind);
    if (rows === undefined) {
      groups.set(item.kind, [rowFor(item)]);
    } else {
      rows.push(rowFor(item));
    }
  }
  for (const [kind, rows] of groups) {
    sections.push({ title: `${kindTitle(kind)} (${rows.length})`, rows, warning: false });
  }

  const subject = options.askedAbout !== undefined ? `${options.symbol} (${options.askedAbout})` : options.symbol;
  const doubt = doubtOf(answer);
  return {
    title: `${QUESTION_TITLE[options.kind]} ${subject}`,
    // Shown as the tool wrote it. Its first word may be UNKNOWN, UNRELIABLE,
    // UNVERIFIED, INCOMPLETE or AMBIGUOUS, and that word is the answer.
    summary: answer.summary !== '' ? answer.summary : 'GraphLume returned no summary.',
    sections,
    resultCount: answer.items.length,
    totalCount: totalRows(answer),
    status: [doubt?.word ?? '', countRows(answer)].filter((s) => s !== '').join(' · '),
  };
}

/**
 * How many rows there are: `3 rows`, from the tool's own count when it gave
 * one, and `showing 50 of 63` when it returned fewer than it counted.
 */
export function countRows(answer: ToolAnswer): string {
  const total = totalRows(answer);
  if (rowsWithheld(answer) > 0) {
    // A next offset with a total no larger than the page contradicts itself;
    // the rows in hand are then the only number worth stating.
    return total > answer.items.length ? `showing ${answer.items.length} of ${total}` : `showing ${answer.items.length}, more exist`;
  }
  return `${total} ${total === 1 ? 'row' : 'rows'}`;
}

/** The tool's count of the rows that exist, and never fewer than it returned. */
function totalRows(answer: ToolAnswer): number {
  return Math.max(answer.page?.total ?? 0, answer.items.length);
}

/**
 * The full report as plain text: the header the extension adds, then the
 * exact text an MCP client would have received. Opened from the view so a
 * note too long for one row can always be read in full.
 */
export function fullReport(model: ResultModel, answer: ToolAnswer, argv: readonly string[]): string {
  const lines: string[] = [];
  lines.push(model.title, '');
  lines.push('Arguments passed to graphlume (as an array; no shell is involved):');
  for (const arg of argv) {
    lines.push('  ' + arg);
  }
  lines.push('');
  const extensionNotes = model.sections.flatMap((s) => s.rows).filter((r): r is LimitRow => r.type === 'limit' && r.author === 'extension');
  if (extensionNotes.length > 0) {
    lines.push('Notes from the extension (not from GraphLume):');
    for (const note of extensionNotes) {
      lines.push('  - ' + note.text);
    }
    lines.push('');
  }
  lines.push('What GraphLume returned, verbatim:', '');
  lines.push(answer.text.trimEnd());
  lines.push('');
  return lines.join('\n');
}
