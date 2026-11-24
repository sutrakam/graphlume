import * as vscode from 'vscode';

import { questionArguments } from './core/args';
import { BoundedCache, InFlight, QueueOverflowError, QuietGate, TaskQueue } from './core/cache';
import { GraphLumeClient } from './core/client';
import type { LensCount, LensTarget, OutlineSymbol } from './core/lens';
import { lensCacheKey, lensCount, lensTargets, lensTitle, lensTooltip } from './core/lens';
import { isCancellation, messageForError, messageForOutcome } from './core/messages';
import { toRepoRelative } from './core/paths';
import { codeLensEnabled, minConfidence, projectRootFor } from './config';
import type { StatusController } from './statusBar';

/** Caller lookups running at once, across all files. */
const MAX_CONCURRENT_LOOKUPS = 2;
/** Lookups allowed to wait. Beyond this the oldest is dropped. */
const MAX_WAITING_LOOKUPS = 80;
/** A lens waits until its document has been left alone this long. */
const TYPING_QUIET_MS = 1_200;
/** One lookup may take this long. */
const LOOKUP_TIMEOUT_MS = 10_000;
/** A failed lookup is not retried for this long. */
const FAILURE_BACKOFF_MS = 30_000;
/** Outline results are retried this many times while a language server starts. */
const MAX_OUTLINE_RETRIES = 2;
const OUTLINE_RETRY_MS = 3_000;

/** What a lens asks "Show callers" to look up when clicked. */
export interface LensRequest {
  readonly uri: string;
  /** 0-based line of the definition. */
  readonly line: number;
  readonly name: string;
}

interface Resolved {
  readonly count: LensCount;
  readonly tooltip: string;
}

/** A lookup that finished without a usable answer. Its message is already worded for the user. */
class LookupFailed extends Error {}

type CachedLookup = { readonly ok: true; readonly value: Resolved } | { readonly ok: false; readonly message: string; readonly at: number };

class CallerLens extends vscode.CodeLens {
  constructor(
    range: vscode.Range,
    readonly uri: vscode.Uri,
    readonly root: string,
    readonly relativePath: string,
    readonly target: LensTarget,
  ) {
    super(range);
  }
}

function categoryOf(kind: vscode.SymbolKind): OutlineSymbol['category'] {
  switch (kind) {
    case vscode.SymbolKind.Function:
    case vscode.SymbolKind.Method:
    case vscode.SymbolKind.Constructor:
      return 'function';
    case vscode.SymbolKind.Class:
    case vscode.SymbolKind.Interface:
    case vscode.SymbolKind.Struct:
    case vscode.SymbolKind.Enum:
      return 'type';
    default:
      return 'other';
  }
}

function toOutline(symbols: readonly (vscode.DocumentSymbol | vscode.SymbolInformation)[]): OutlineSymbol[] {
  return symbols.map((s) => {
    if ('selectionRange' in s) {
      return { name: s.name, category: categoryOf(s.kind), line: s.selectionRange.start.line, children: toOutline(s.children) };
    }
    return { name: s.name, category: categoryOf(s.kind), line: s.location.range.start.line };
  });
}

/**
 * The 1-based line sent to select a lens's definition, or undefined to
 * select by file alone.
 *
 * The line comes from the editor's outline, which follows the buffer, while
 * the tool matches it against the index. After edits the two drift apart, so
 * the line is sent only where the file alone cannot decide: a name the file
 * defines more than once. If it then matches nothing, the tool lists the
 * candidates and the lens says the callers are unknown.
 */
function selectingLine(target: LensTarget): number | undefined {
  return target.sharedName ? target.line + 1 : undefined;
}

/**
 * A CodeLens with the caller count above function and type definitions.
 *
 * Off unless `graphlume.codeLens.enabled` is set. Built so that typing never
 * starts a process:
 *
 *  - where the lenses go comes from the editor's own outline, computed once
 *    per document version and capped per file;
 *  - a count is looked up only when VS Code asks to resolve a lens, which it
 *    does only for lenses scrolled into view;
 *  - a count is cached until the index is rebuilt, so it is looked up once,
 *    not once per edit;
 *  - a lookup waits until the document has been quiet, so a name that is
 *    still being typed is never asked about;
 *  - at most two lookups run at a time, and identical ones share a process.
 */
export class CallerLensProvider implements vscode.CodeLensProvider<CallerLens>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<void>();
  private readonly outlines = new Map<string, { version: number; targets: LensTarget[] }>();
  private readonly outlineRetries = new Map<string, number>();
  private readonly counts = new BoundedCache<CachedLookup>(4000);
  private readonly queue = new TaskQueue(MAX_CONCURRENT_LOOKUPS, MAX_WAITING_LOOKUPS);
  private readonly inFlight = new InFlight<Resolved>();
  private readonly quiet = new QuietGate(TYPING_QUIET_MS);
  private readonly disposables: vscode.Disposable[] = [];
  private readonly retryTimers = new Set<NodeJS.Timeout>();

  readonly onDidChangeCodeLenses = this.changed.event;

  constructor(
    private readonly client: GraphLumeClient,
    private readonly status: StatusController,
  ) {
    this.disposables.push(
      this.changed,
      vscode.workspace.onDidChangeTextDocument((event) => {
        if (event.contentChanges.length > 0) {
          this.quiet.touch(event.document.uri.toString());
        }
      }),
      vscode.workspace.onDidCloseTextDocument((document) => {
        const key = document.uri.toString();
        this.outlines.delete(key);
        this.outlineRetries.delete(key);
        this.quiet.forget(key);
      }),
      // A rebuilt index changes the cache key, so old counts are simply never
      // read again; the lenses only need to be asked for once more.
      status.onDidChange(() => this.changed.fire()),
    );
  }

  dispose(): void {
    for (const timer of this.retryTimers) {
      clearTimeout(timer);
    }
    this.retryTimers.clear();
    for (const d of this.disposables.splice(0)) {
      d.dispose();
    }
  }

  /** Ask VS Code to request the lenses again, for example after a settings change. */
  refresh(): void {
    this.changed.fire();
  }

  async provideCodeLenses(document: vscode.TextDocument, token: vscode.CancellationToken): Promise<CallerLens[]> {
    if (document.uri.scheme !== 'file' || !codeLensEnabled(document.uri)) {
      return [];
    }
    const root = projectRootFor(document.uri);
    if (root === undefined || !this.status.isQueryable(root)) {
      return [];
    }
    const relativePath = toRepoRelative(root, document.uri.fsPath);
    if (relativePath === undefined) {
      return [];
    }
    const targets = await this.targetsFor(document, token);
    return targets.map(
      (target) => new CallerLens(new vscode.Range(target.line, 0, target.line, 0), document.uri, root, relativePath, target),
    );
  }

  private async targetsFor(document: vscode.TextDocument, token: vscode.CancellationToken): Promise<LensTarget[]> {
    const key = document.uri.toString();
    const cached = this.outlines.get(key);
    if (cached !== undefined && cached.version === document.version) {
      return cached.targets;
    }
    let symbols: readonly (vscode.DocumentSymbol | vscode.SymbolInformation)[] | undefined;
    try {
      symbols = await vscode.commands.executeCommand<(vscode.DocumentSymbol | vscode.SymbolInformation)[] | undefined>(
        'vscode.executeDocumentSymbolProvider',
        document.uri,
      );
    } catch {
      symbols = undefined;
    }
    if (token.isCancellationRequested) {
      return [];
    }
    if (symbols === undefined || symbols.length === 0) {
      // No outline yet: the language server may still be starting. Ask again
      // a couple of times, then leave the file without lenses.
      const tries = this.outlineRetries.get(key) ?? 0;
      if (tries < MAX_OUTLINE_RETRIES) {
        this.outlineRetries.set(key, tries + 1);
        const timer = setTimeout(() => {
          this.retryTimers.delete(timer);
          this.changed.fire();
        }, OUTLINE_RETRY_MS);
        this.retryTimers.add(timer);
      }
      return [];
    }
    const targets = lensTargets(toOutline(symbols));
    this.outlines.set(key, { version: document.version, targets });
    return targets;
  }

  async resolveCodeLens(lens: CallerLens, token: vscode.CancellationToken): Promise<CallerLens> {
    const request: LensRequest = { uri: lens.uri.toString(), line: lens.target.line, name: lens.target.name };
    const floor = minConfidence(lens.uri);
    const key = lensCacheKey(lens.root, this.status.token(lens.root), floor, lens.relativePath, lens.target.name, selectingLine(lens.target));

    const apply = (title: string, tooltip: string): CallerLens => {
      lens.command = { title, tooltip, command: 'graphlume.showCallers', arguments: [request] };
      return lens;
    };

    const cached = this.counts.get(key);
    if (cached !== undefined) {
      if (cached.ok) {
        return apply(lensTitle(cached.value.count, lens.target), cached.value.tooltip);
      }
      if (Date.now() - cached.at < FAILURE_BACKOFF_MS) {
        return apply('GraphLume: callers unavailable', cached.message);
      }
    }

    const controller = new AbortController();
    const subscription = token.onCancellationRequested(() => controller.abort());
    if (token.isCancellationRequested) {
      controller.abort();
    }
    try {
      await this.quiet.whenQuiet(lens.uri.toString(), controller.signal);
      const value = await this.inFlight.join(key, controller.signal, (shared) =>
        this.queue.run((signal) => this.lookup(lens, floor, signal), shared),
      );
      this.counts.set(key, { ok: true, value });
      return apply(lensTitle(value.count, lens.target), value.tooltip);
    } catch (err) {
      if (isCancellation(err) || err instanceof QueueOverflowError) {
        // VS Code no longer wants this lens, or newer lenses took its place
        // in line. Neither is a failure worth remembering; the lens still
        // needs a command to be valid.
        return apply('GraphLume: callers…', 'The lookup did not run. Click to list the callers.');
      }
      const message = err instanceof LookupFailed ? err.message : (messageForError(err)?.text ?? 'The lookup failed.');
      this.counts.set(key, { ok: false, message, at: Date.now() });
      return apply('GraphLume: callers unavailable', message);
    } finally {
      subscription.dispose();
    }
  }

  private async lookup(lens: CallerLens, floor: ReturnType<typeof minConfidence>, signal: AbortSignal): Promise<Resolved> {
    const line = selectingLine(lens.target);
    const call = await this.client.callTool(
      lens.root,
      'callers',
      questionArguments('callers', { name: lens.target.name, path: lens.relativePath, ...(line !== undefined ? { line } : {}) }, floor),
      { signal, timeoutMs: LOOKUP_TIMEOUT_MS },
    );
    if (call.outcome.kind !== 'answer') {
      throw new LookupFailed(messageForOutcome(call.outcome).text);
    }
    const answer = call.outcome.answer;
    this.status.noteProvenance(lens.root, answer.provenance);
    // The title is built when the lens is drawn: it also depends on whether
    // the file has another definition with the same name.
    const count = lensCount(answer, lens.relativePath);
    return { count, tooltip: lensTooltip(count, answer, floor) };
  }
}
