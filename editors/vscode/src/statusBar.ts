import * as fs from 'node:fs';
import * as vscode from 'vscode';

import { GraphLumeClient } from './core/client';
import { isCancellation } from './core/messages';
import type { Provenance } from './core/result';
import { BinaryNotFoundError } from './core/runner';
import type { IndexFile, IndexStatus, StatusPresentation, WorkspaceIndexState } from './core/status';
import { indexToken, isQueryable, presentStatus } from './core/status';
import { binaryPath, currentProjectRoot } from './config';

/** Re-render the age in the status bar this often. No process is started for it. */
const AGE_TICK_MS = 60_000;
/** Wait this long after the index file changes before reading status again. */
const WATCH_DEBOUNCE_MS = 1_500;
/** Refresh on window focus at most this often. */
const FOCUS_REFRESH_MS = 30_000;

/** Size and modification time of the index database, or undefined when it cannot be read. */
function statIndexFile(indexPath: string): IndexFile | undefined {
  if (indexPath === '') {
    return undefined;
  }
  try {
    const stat = fs.statSync(indexPath);
    return stat.isFile() ? { modifiedMs: stat.mtimeMs, size: stat.size } : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Owns the status bar item and the extension's knowledge of the index:
 * whether one exists for the current project, when it was built, and whether
 * it was built from the commit that is checked out.
 */
export class StatusController implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;
  private readonly changed = new vscode.EventEmitter<void>();
  private readonly disposables: vscode.Disposable[] = [];
  private readonly lifetime = new AbortController();

  private root: string | undefined;
  private state: WorkspaceIndexState = { kind: 'checking' };
  private running: Promise<void> | undefined;
  private queued: Promise<void> | undefined;
  private lastRefresh = 0;
  private watchTimer: NodeJS.Timeout | undefined;

  /** Fires when the index state changes in a way cached answers depend on. */
  readonly onDidChange = this.changed.event;

  constructor(private readonly client: GraphLumeClient) {
    this.item = vscode.window.createStatusBarItem('graphlume.status', vscode.StatusBarAlignment.Left, 0);
    this.item.name = 'GraphLume index';
    this.item.command = 'graphlume.showStatus';
    this.render();
    this.item.show();

    const tick = setInterval(() => this.render(), AGE_TICK_MS);
    this.disposables.push(
      this.item,
      this.changed,
      { dispose: () => clearInterval(tick) },
      { dispose: () => this.lifetime.abort() },
      { dispose: () => clearTimeout(this.watchTimer) },
      vscode.window.onDidChangeActiveTextEditor(() => {
        if (currentProjectRoot() !== this.root) {
          void this.refresh();
        }
      }),
      vscode.workspace.onDidChangeWorkspaceFolders(() => {
        this.watchFolders();
        void this.refresh();
      }),
      vscode.window.onDidChangeWindowState((state) => {
        // HEAD can move without the index file changing, so look again when
        // the user comes back to the window.
        if (state.focused && Date.now() - this.lastRefresh > FOCUS_REFRESH_MS) {
          void this.refresh();
        }
      }),
    );
    this.watchFolders();
  }

  private readonly watchers: vscode.Disposable[] = [];

  private watchFolders(): void {
    for (const w of this.watchers.splice(0)) {
      w.dispose();
    }
    for (const folder of vscode.workspace.workspaceFolders ?? []) {
      if (folder.uri.scheme !== 'file') continue;
      const watcher = vscode.workspace.createFileSystemWatcher(new vscode.RelativePattern(folder, '.graphlume/index.db'));
      const poke = (): void => {
        clearTimeout(this.watchTimer);
        this.watchTimer = setTimeout(() => void this.refresh(), WATCH_DEBOUNCE_MS);
      };
      this.watchers.push(watcher, watcher.onDidChange(poke), watcher.onDidCreate(poke), watcher.onDidDelete(poke));
    }
  }

  dispose(): void {
    for (const w of this.watchers.splice(0)) {
      w.dispose();
    }
    for (const d of this.disposables.splice(0)) {
      d.dispose();
    }
  }

  /** The project the status bar is describing. */
  get projectRoot(): string | undefined {
    return this.root;
  }

  /** The last status read for `root`, when it is the project being tracked. */
  statusFor(root: string): IndexStatus | undefined {
    return this.root === root && this.state.kind === 'status' ? this.state.status : undefined;
  }

  /** True when the tracked project is `root` and has an index a tool can answer from. */
  isQueryable(root: string): boolean {
    return isQueryable(this.statusFor(root));
  }

  /** Changes whenever the index for `root` is rebuilt or updated. */
  token(root: string): string {
    return this.root === root ? this.currentToken() : indexToken(undefined);
  }

  private currentToken(): string {
    return this.state.kind === 'status' ? indexToken(this.state.status, this.state.indexFile) : indexToken(undefined);
  }

  presentation(): StatusPresentation {
    return presentStatus(this.state, Date.now());
  }

  /**
   * Takes the provenance from a tool answer. Every answer carries one, so
   * the status bar learns that HEAD moved without starting a process.
   */
  noteProvenance(root: string, provenance: Provenance | undefined): void {
    if (provenance === undefined || this.root !== root || this.state.kind !== 'status') {
      return;
    }
    this.state = {
      kind: 'status',
      status: this.state.status,
      provenance,
      ...(this.state.indexFile !== undefined ? { indexFile: this.state.indexFile } : {}),
    };
    this.render();
  }

  /**
   * Reads the status again. A call made while a read is running is folded
   * into ONE further read, and its promise settles when that read does, so
   * awaiting refresh() always yields a state read after the call was made.
   */
  refresh(): Promise<void> {
    if (this.running === undefined) {
      this.running = this.refreshOnce().finally(() => {
        this.running = undefined;
      });
      return this.running;
    }
    this.queued ??= this.running
      .catch(() => undefined)
      .then(() => {
        this.queued = undefined;
        return this.refresh();
      });
    return this.queued;
  }

  private async refreshOnce(): Promise<void> {
    const root = currentProjectRoot();
    const before = this.root === root ? this.currentToken() : undefined;
    const beforeKind = this.state.kind;
    this.root = root;
    this.lastRefresh = Date.now();

    let next: WorkspaceIndexState;
    if (root === undefined) {
      next = { kind: 'no-workspace' };
    } else {
      next = await this.read(root);
    }
    if (this.lifetime.signal.aborted) {
      return;
    }
    this.state = next;
    this.render();
    const after = this.currentToken();
    if (before !== after || beforeKind !== next.kind) {
      this.changed.fire();
    }
  }

  private async read(root: string): Promise<WorkspaceIndexState> {
    const signal = this.lifetime.signal;
    let status: IndexStatus;
    try {
      status = await this.client.status(root, { signal });
    } catch (err) {
      if (err instanceof BinaryNotFoundError) {
        return { kind: 'binary-missing', binary: binaryPath() };
      }
      if (isCancellation(err)) {
        return this.state;
      }
      return { kind: 'error', message: err instanceof Error ? err.message : String(err) };
    }
    const indexFile = statIndexFile(status.indexPath);
    const base = { kind: 'status' as const, status, ...(indexFile !== undefined ? { indexFile } : {}) };
    if (!isQueryable(status)) {
      return base;
    }
    // `status` does not compare the index with HEAD; a tool answer does.
    try {
      const call = await this.client.callTool(root, 'overview', {}, { signal });
      if (call.outcome.kind === 'answer' && call.outcome.answer.provenance !== undefined) {
        return { ...base, provenance: call.outcome.answer.provenance };
      }
    } catch {
      // The status itself was read; only the HEAD comparison is missing, and
      // the presentation says so.
    }
    return base;
  }

  private render(): void {
    const view = this.presentation();
    this.item.text = `$(${view.icon}) ${view.text}`;
    this.item.tooltip = [...view.detail, '', 'Click for actions.'].join('\n');
    this.item.backgroundColor =
      view.severity === 'error' ? new vscode.ThemeColor('statusBarItem.errorBackground') : undefined;
    this.item.accessibilityInformation = { label: `${view.text}. ${view.detail.join(' ')}` };
  }
}
