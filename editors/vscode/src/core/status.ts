// Reads `graphlume --json status` and turns it, with the provenance a tool
// call reports, into what the status bar shows.
//
// `status` answers "is there an index, and when was it built". It does not
// compare the index with HEAD. That comparison comes from the `provenance`
// block on a tool result, and it compares COMMITS only: a matching commit
// never means the working tree matches. The wording here keeps to that.

import type { Provenance } from './result';

export interface IndexStatus {
  readonly project: string;
  readonly indexPath: string;
  /** absent, empty, indexing, ready, degraded or failed. Unknown values pass through. */
  readonly indexStatus: string;
  readonly indexRun?: number;
  readonly gitRevision?: string;
  /** RFC 3339, as printed. */
  readonly lastIndexed?: string;
  readonly files: number;
  readonly symbols: number;
  readonly relationships: number;
  readonly parseFailures: number;
}

export class StatusError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'StatusError';
  }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function count(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/** Parses the JSON printed by `graphlume --json status`. */
export function parseStatus(stdout: string): IndexStatus {
  let raw: unknown;
  try {
    raw = JSON.parse(stdout);
  } catch (err) {
    throw new StatusError(`graphlume status did not print JSON: ${err instanceof Error ? err.message : String(err)}`);
  }
  if (!isObject(raw) || typeof raw['index_status'] !== 'string' || raw['index_status'] === '') {
    throw new StatusError('graphlume status printed JSON without an index_status field.');
  }
  const out: {
    project: string;
    indexPath: string;
    indexStatus: string;
    indexRun?: number;
    gitRevision?: string;
    lastIndexed?: string;
    files: number;
    symbols: number;
    relationships: number;
    parseFailures: number;
  } = {
    project: typeof raw['project'] === 'string' ? raw['project'] : '',
    indexPath: typeof raw['index_path'] === 'string' ? raw['index_path'] : '',
    indexStatus: raw['index_status'],
    files: count(raw['files']),
    symbols: count(raw['symbols']),
    relationships: count(raw['relationships']),
    parseFailures: count(raw['parse_failures']),
  };
  if (typeof raw['index_run'] === 'number') out.indexRun = raw['index_run'];
  if (typeof raw['git_revision'] === 'string' && raw['git_revision'] !== '') out.gitRevision = raw['git_revision'];
  if (typeof raw['last_indexed'] === 'string' && raw['last_indexed'] !== '') out.lastIndexed = raw['last_indexed'];
  return out;
}

/**
 * The index database file as the extension sees it on disk.
 *
 * `graphlume status` reports when the index run last finished. GraphLume
 * up to 1.0.1 wrote that once, at the full run, and an incremental
 * `graphlume index` left it alone, so on its own it made a freshly updated
 * index look as old as its first build. Later versions move it on every
 * incremental update, but `graphlume resolve` and `scip import` change the
 * graph without moving it in any version. The file's modification time moves
 * on every write, and reading the index does not move it, so the newer of
 * the two is the honest age whichever binary is installed.
 */
export interface IndexFile {
  readonly modifiedMs: number;
  readonly size: number;
}

/**
 * A value that changes whenever the index is rebuilt or updated. Cached
 * answers are keyed on it, so a re-index invalidates them without any
 * bookkeeping.
 */
export function indexToken(status: IndexStatus | undefined, file?: IndexFile): string {
  if (status === undefined) {
    return 'none';
  }
  return [
    status.indexStatus,
    status.indexRun ?? '',
    status.lastIndexed ?? '',
    status.symbols,
    status.relationships,
    file !== undefined ? `${Math.floor(file.modifiedMs)}:${file.size}` : '',
  ].join('|');
}

/** True when there is an index a tool can answer from. */
export function isQueryable(status: IndexStatus | undefined): boolean {
  return status !== undefined && (status.indexStatus === 'ready' || status.indexStatus === 'degraded');
}

/** "just now", "5m", "3h", "2d". Coarse on purpose: this is a status bar. */
export function formatAge(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) {
    return 'unknown age';
  }
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m old`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h old`;
  return `${Math.floor(hours / 24)}d old`;
}

function shortSha(sha: string | undefined): string {
  return sha === undefined ? 'unknown' : sha.slice(0, 8);
}

export type Severity = 'ok' | 'warning' | 'error';

export interface StatusPresentation {
  /** Status bar text, without an icon. */
  readonly text: string;
  /** A codicon name. */
  readonly icon: string;
  readonly severity: Severity;
  /** Plain-text lines for the tooltip and the status menu. */
  readonly detail: readonly string[];
  /** True when running the index command is the obvious next step. */
  readonly suggestIndex: boolean;
}

export type WorkspaceIndexState =
  | { readonly kind: 'checking' }
  | { readonly kind: 'no-workspace' }
  | { readonly kind: 'binary-missing'; readonly binary: string }
  | { readonly kind: 'error'; readonly message: string }
  | { readonly kind: 'status'; readonly status: IndexStatus; readonly provenance?: Provenance; readonly indexFile?: IndexFile };

/** What the status bar shows for a workspace's index, at time `now`. */
export function presentStatus(state: WorkspaceIndexState, now: number): StatusPresentation {
  switch (state.kind) {
    case 'checking':
      return { text: 'GraphLume: checking', icon: 'sync', severity: 'ok', detail: ['Checking the index.'], suggestIndex: false };
    case 'no-workspace':
      return {
        text: 'GraphLume: no folder',
        icon: 'circle-slash',
        severity: 'ok',
        detail: ['Open a folder to use GraphLume.'],
        suggestIndex: false,
      };
    case 'binary-missing':
      return {
        text: 'GraphLume: binary not found',
        icon: 'error',
        severity: 'error',
        detail: [
          `The graphlume binary was not found (tried "${state.binary}").`,
          'Install GraphLume, or set "graphlume.path" in your user settings to the full path of the binary.',
        ],
        suggestIndex: false,
      };
    case 'error':
      return {
        text: 'GraphLume: status failed',
        icon: 'error',
        severity: 'error',
        detail: ['graphlume status failed:', state.message],
        suggestIndex: false,
      };
    case 'status':
      return presentIndexStatus(state.status, state.provenance, state.indexFile, now);
  }
}

/** An index file written this much later than the reported run was updated in place. */
const UPDATED_IN_PLACE_MS = 60_000;

function presentIndexStatus(
  status: IndexStatus,
  provenance: Provenance | undefined,
  indexFile: IndexFile | undefined,
  now: number,
): StatusPresentation {
  switch (status.indexStatus) {
    case 'absent':
      return {
        text: 'GraphLume: no index',
        icon: 'circle-slash',
        severity: 'warning',
        detail: ['This workspace has no GraphLume index.', 'Run "GraphLume: Index this workspace in a terminal" to build one.'],
        suggestIndex: true,
      };
    case 'empty':
      return {
        text: 'GraphLume: not indexed',
        icon: 'circle-slash',
        severity: 'warning',
        detail: ['The index database exists but nothing has been indexed yet.', 'Run "GraphLume: Index this workspace in a terminal".'],
        suggestIndex: true,
      };
    case 'indexing':
      return {
        text: 'GraphLume: indexing',
        icon: 'sync',
        severity: 'ok',
        detail: [
          'An index is being built. Answers are unavailable until it finishes.',
          'If no index is running, a previous one was interrupted: run the index command again.',
        ],
        suggestIndex: false,
      };
    case 'failed':
      return {
        text: 'GraphLume: index failed',
        icon: 'error',
        severity: 'error',
        detail: ['The last index run failed. Run the index command again and read its output.'],
        suggestIndex: true,
      };
    default:
      break;
  }

  const detail: string[] = [];
  const built = status.lastIndexed !== undefined ? Date.parse(status.lastIndexed) : Number.NaN;
  // The newer of the two: the run's own timestamp, and the last write to the
  // index file, which is what moves when an index is updated in place.
  const updatedInPlace =
    indexFile !== undefined && (Number.isNaN(built) || indexFile.modifiedMs > built + UPDATED_IN_PLACE_MS);
  const updated = updatedInPlace ? indexFile.modifiedMs : built;
  const age = Number.isNaN(updated) ? undefined : formatAge(now - updated);
  detail.push(`${status.files} files, ${status.symbols} symbols, ${status.relationships} relationships indexed.`);
  if (status.lastIndexed !== undefined) {
    detail.push(`GraphLume reports its index run last finished ${status.lastIndexed}${!updatedInPlace && age !== undefined ? ` (${age})` : ''}.`);
  }
  if (updatedInPlace) {
    detail.push(
      `The index file was last written ${new Date(indexFile.modifiedMs).toISOString()}${age !== undefined ? ` (${age})` : ''}. ` +
        'That is later than the run timestamp, which `resolve` never moves and older GraphLume versions do not move on an incremental update, so the age shown is the file\'s.',
    );
  }
  if (status.gitRevision !== undefined) {
    detail.push(`Built from commit ${shortSha(status.gitRevision)}.`);
  }

  let severity: Severity = 'ok';
  let icon = 'database';
  const parts: string[] = [];
  let suggestIndex = false;

  if (status.indexStatus === 'degraded') {
    severity = 'warning';
    icon = 'warning';
    parts.push('degraded');
    detail.push(`The index is usable but ${status.parseFailures} file(s) failed to parse.`);
  } else if (status.indexStatus !== 'ready') {
    severity = 'warning';
    icon = 'warning';
    parts.push(status.indexStatus);
    detail.push(`GraphLume reports the index status "${status.indexStatus}", which this extension does not recognise.`);
  }

  if (provenance === undefined) {
    detail.push('Whether the index matches HEAD could not be determined.');
  } else if (provenance.headMatchesBuild) {
    detail.push('The index was built from the commit that is checked out.');
  } else if (provenance.headSha !== undefined && provenance.builtAtSha !== undefined) {
    // "Not at HEAD" rather than "behind": the checkout may be older than the index.
    severity = 'warning';
    icon = 'warning';
    parts.push('not at HEAD');
    suggestIndex = true;
  }
  // The tool's own sentence, always: it names the two commits when they
  // differ, and says what a commit comparison cannot cover when they match.
  if (provenance !== undefined && provenance.caveat !== '') {
    detail.push(provenance.caveat);
  } else {
    detail.push('Uncommitted changes are never reflected in the index.');
  }

  if (age !== undefined) {
    parts.push(age);
  }
  const suffix = parts.length > 0 ? parts.join(', ') : 'ready';
  return { text: `GraphLume: ${suffix}`, icon, severity, detail, suggestIndex };
}
