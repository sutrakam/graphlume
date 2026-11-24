// A small stand-in for the `vscode` module, so the extension's own code can
// be run under node:test: activation, the commands, the results tree, the
// CodeLens provider and the status bar.
//
// It records what the extension asks the editor to do and lets a test script
// the editor's answers. It is NOT VS Code: it proves the extension's logic
// and its use of the API as modelled here, not how the real editor renders
// or schedules anything.

/* eslint-disable @typescript-eslint/no-explicit-any */

import * as fs from 'node:fs';
import * as path from 'node:path';

type Listener<T> = (event: T) => unknown;

class EventEmitter<T> {
  private listeners: Listener<T>[] = [];
  readonly event = (listener: Listener<T>): { dispose(): void } => {
    this.listeners.push(listener);
    return { dispose: () => void (this.listeners = this.listeners.filter((l) => l !== listener)) };
  };
  fire(event: T): void {
    for (const listener of [...this.listeners]) listener(event);
  }
  dispose(): void {
    this.listeners = [];
  }
}

class Position {
  constructor(
    readonly line: number,
    readonly character: number,
  ) {}
}

class Range {
  readonly start: Position;
  readonly end: Position;
  constructor(a: number | Position, b: number | Position, c?: number, d?: number) {
    if (typeof a === 'number') {
      this.start = new Position(a, b as number);
      this.end = new Position(c as number, d as number);
    } else {
      this.start = a;
      this.end = b as Position;
    }
  }
  get isEmpty(): boolean {
    return this.start.line === this.end.line && this.start.character === this.end.character;
  }
  get isSingleLine(): boolean {
    return this.start.line === this.end.line;
  }
}

class Selection extends Range {
  get active(): Position {
    return this.end;
  }
}

class Uri {
  private constructor(
    readonly scheme: string,
    readonly path: string,
  ) {}
  static file(p: string): Uri {
    return new Uri('file', p);
  }
  static from(parts: { scheme: string; path: string }): Uri {
    return new Uri(parts.scheme, parts.path);
  }
  static parse(value: string): Uri {
    const match = /^([a-z][a-z0-9+.-]*):(?:\/\/)?(.*)$/iu.exec(value);
    if (match === null) throw new Error(`not a uri: ${value}`);
    return new Uri(match[1] as string, decodeURIComponent(match[2] as string));
  }
  get fsPath(): string {
    return this.path;
  }
  toString(): string {
    return `${this.scheme}://${encodeURI(this.path)}`;
  }
}

class ThemeIcon {
  constructor(
    readonly id: string,
    readonly color?: ThemeColor,
  ) {}
}
class ThemeColor {
  constructor(readonly id: string) {}
}
class TreeItem {
  description?: string;
  tooltip?: string;
  iconPath?: ThemeIcon;
  command?: { command: string; title: string; arguments?: unknown[] };
  accessibilityInformation?: { label: string };
  constructor(
    readonly label: string,
    readonly collapsibleState: number,
  ) {}
}
class CodeLens {
  command?: { command: string; title: string; tooltip?: string; arguments?: unknown[] };
  constructor(readonly range: Range) {}
}
class Task {
  presentationOptions: unknown;
  constructor(
    readonly definition: Record<string, unknown>,
    readonly scope: unknown,
    readonly name: string,
    readonly source: string,
    readonly execution: ProcessExecution,
    readonly problemMatchers: string[],
  ) {}
}
class ProcessExecution {
  constructor(
    readonly process: string,
    readonly args: string[],
    readonly options: { cwd?: string },
  ) {}
}
class RelativePattern {
  constructor(
    readonly base: unknown,
    readonly pattern: string,
  ) {}
}
class DocumentSymbol {
  children: DocumentSymbol[] = [];
  constructor(
    readonly name: string,
    readonly detail: string,
    readonly kind: number,
    readonly range: Range,
    readonly selectionRange: Range,
  ) {}
}

export class FakeDocument {
  version = 1;
  private readonly lines: string[];
  constructor(
    readonly uri: Uri,
    private readonly text: string,
  ) {
    this.lines = text.split(/\r?\n/u);
  }
  get lineCount(): number {
    return this.lines.length;
  }
  getText(range?: Range): string {
    if (range === undefined) return this.text;
    return (this.lines[range.start.line] ?? '').slice(range.start.character, range.end.character);
  }
  lineAt(line: number): { text: string } {
    return { text: this.lines[line] ?? '' };
  }
  getWordRangeAtPosition(position: Position): Range | undefined {
    const text = this.lines[position.line] ?? '';
    for (const match of text.matchAll(/[\p{L}\p{N}_$]+/gu)) {
      const start = match.index;
      const end = start + match[0].length;
      if (position.character >= start && position.character <= end) {
        return new Range(position.line, start, position.line, end);
      }
    }
    return undefined;
  }
}

export interface Shown {
  readonly document: FakeDocument;
  readonly options: { selection?: Range } | undefined;
}

export interface Message {
  readonly severity: 'info' | 'warning' | 'error';
  readonly text: string;
  readonly actions: string[];
}

/** What a test can script and inspect. */
export class Control {
  settings: Record<string, unknown> = {};
  workspaceFolders: { uri: Uri; name: string; index: number }[] = [];
  activeTextEditor: { document: FakeDocument; selection: Selection } | undefined;

  readonly commands = new Map<string, (...args: any[]) => unknown>();
  readonly messages: Message[] = [];
  readonly quickPicks: { items: any[]; options: any }[] = [];
  readonly shown: Shown[] = [];
  readonly tasks: Task[] = [];
  readonly openedSettings: unknown[] = [];
  readonly statusBarItems: any[] = [];
  readonly treeViews = new Map<string, { view: any; provider: any }>();
  readonly contentProviders = new Map<string, any>();
  readonly codeLensProviders: any[] = [];

  /** Answers a notification: return the label of the button to press. */
  answerMessage: (message: Message) => string | undefined = () => undefined;
  /** Answers a quick pick: return the chosen item. */
  answerQuickPick: (items: any[], options: any) => any = () => undefined;
  definitionProvider: (uri: Uri, position: Position) => unknown[] = () => [];
  symbolProvider: (uri: Uri) => unknown[] | undefined = () => undefined;
  taskExitCode: (task: Task) => number | undefined = () => 0;

  readonly onDidChangeConfiguration = new EventEmitter<{ affectsConfiguration(section: string): boolean }>();
  readonly onDidChangeActiveTextEditor = new EventEmitter<unknown>();
  readonly onDidChangeTextDocument = new EventEmitter<unknown>();
  readonly onDidEndTaskProcess = new EventEmitter<unknown>();
  readonly onDidEndTask = new EventEmitter<unknown>();

  openEditor(file: string, line: number, character: number): FakeDocument {
    const document = new FakeDocument(Uri.file(file), fs.readFileSync(file, 'utf8'));
    this.activeTextEditor = { document, selection: new Selection(line, character, line, character) };
    this.onDidChangeActiveTextEditor.fire(this.activeTextEditor);
    return document;
  }

  setSetting(key: string, value: unknown): void {
    this.settings[key] = value;
    this.onDidChangeConfiguration.fire({ affectsConfiguration: (section) => key.startsWith(section) });
  }

  run(command: string, ...args: unknown[]): unknown {
    const handler = this.commands.get(command);
    if (handler === undefined) throw new Error(`command not registered: ${command}`);
    return handler(...args);
  }

  /** The tree of the results view, flattened to what a user would read. */
  tree(viewId: string): { title: string; message: string; description: string; rows: { depth: number; item: TreeItem; node: unknown }[] } {
    const entry = this.treeViews.get(viewId);
    if (entry === undefined) throw new Error(`no tree view ${viewId}`);
    const rows: { depth: number; item: TreeItem; node: unknown }[] = [];
    const walk = (node: unknown, depth: number): void => {
      for (const child of entry.provider.getChildren(node) as unknown[]) {
        rows.push({ depth, item: entry.provider.getTreeItem(child) as TreeItem, node: child });
        walk(child, depth + 1);
      }
    };
    walk(undefined, 0);
    return { title: entry.view.title ?? '', message: entry.view.message ?? '', description: entry.view.description ?? '', rows };
  }
}

const disposable = { dispose(): void {} };

export function createFakeVscode(): { api: Record<string, unknown>; control: Control } {
  const control = new Control();

  const notify =
    (severity: Message['severity']) =>
    (text: string, ...actions: string[]): Promise<string | undefined> => {
      const message = { severity, text, actions };
      control.messages.push(message);
      return Promise.resolve(control.answerMessage(message));
    };

  const builtin = async (command: string, args: unknown[]): Promise<unknown> => {
    if (command === 'vscode.executeDefinitionProvider') {
      return control.definitionProvider(args[0] as Uri, args[1] as Position);
    }
    if (command === 'vscode.executeDocumentSymbolProvider') {
      return control.symbolProvider(args[0] as Uri);
    }
    if (command.endsWith('.focus')) {
      return undefined;
    }
    if (command === 'workbench.action.openSettings') {
      control.openedSettings.push(args[0]);
      return undefined;
    }
    throw new Error(`command not found: ${command}`);
  };

  const api = {
    EventEmitter,
    Position,
    Range,
    Selection,
    Uri,
    ThemeIcon,
    ThemeColor,
    TreeItem,
    CodeLens,
    Task,
    ProcessExecution,
    RelativePattern,
    DocumentSymbol,
    TreeItemCollapsibleState: { None: 0, Collapsed: 1, Expanded: 2 },
    StatusBarAlignment: { Left: 1, Right: 2 },
    ProgressLocation: { Notification: 15 },
    ViewColumn: { Beside: -2 },
    QuickPickItemKind: { Separator: -1, Default: 0 },
    TaskRevealKind: { Always: 1 },
    TaskPanelKind: { Shared: 1 },
    TaskScope: { Workspace: 2 },
    SymbolKind: { Class: 4, Method: 5, Constructor: 8, Enum: 9, Interface: 10, Function: 11, Variable: 12, Struct: 22 },

    window: {
      get activeTextEditor() {
        return control.activeTextEditor;
      },
      createStatusBarItem: (id: string) => {
        const item = { id, text: '', tooltip: '', command: '', name: '', visible: false, show: () => void (item.visible = true), dispose() {} };
        control.statusBarItems.push(item);
        return item;
      },
      createTreeView: (id: string, options: { treeDataProvider: unknown }) => {
        const view = { title: '', message: '', description: '', dispose() {} };
        control.treeViews.set(id, { view, provider: options.treeDataProvider });
        return view;
      },
      showInformationMessage: notify('info'),
      showWarningMessage: notify('warning'),
      showErrorMessage: notify('error'),
      showQuickPick: (items: any[], options: any) => {
        control.quickPicks.push({ items, options });
        return Promise.resolve(control.answerQuickPick(items, options));
      },
      withProgress: (_options: unknown, task: (progress: unknown, token: unknown) => Promise<unknown>) =>
        task({ report() {} }, { isCancellationRequested: false, onCancellationRequested: () => disposable }),
      showTextDocument: (document: FakeDocument, options?: { selection?: Range }) => {
        control.shown.push({ document, options });
        return Promise.resolve({ document });
      },
      onDidChangeActiveTextEditor: control.onDidChangeActiveTextEditor.event,
      onDidChangeWindowState: () => disposable,
    },

    workspace: {
      get workspaceFolders() {
        return control.workspaceFolders;
      },
      getWorkspaceFolder: (uri: Uri) =>
        control.workspaceFolders.find((f) => uri.fsPath === f.uri.fsPath || uri.fsPath.startsWith(f.uri.fsPath + path.sep)),
      getConfiguration: (section: string) => ({
        get: (key: string, fallback?: unknown) => control.settings[`${section}.${key}`] ?? fallback,
      }),
      onDidChangeConfiguration: control.onDidChangeConfiguration.event,
      onDidChangeWorkspaceFolders: () => disposable,
      onDidChangeTextDocument: control.onDidChangeTextDocument.event,
      onDidCloseTextDocument: () => disposable,
      createFileSystemWatcher: () => ({ onDidChange: () => disposable, onDidCreate: () => disposable, onDidDelete: () => disposable, dispose() {} }),
      registerTextDocumentContentProvider: (scheme: string, provider: unknown) => {
        control.contentProviders.set(scheme, provider);
        return disposable;
      },
      openTextDocument: async (uri: Uri) => {
        if (uri.scheme === 'file') {
          return new FakeDocument(uri, fs.readFileSync(uri.fsPath, 'utf8'));
        }
        const provider = control.contentProviders.get(uri.scheme);
        if (provider === undefined) throw new Error(`no provider for ${uri.scheme}`);
        return new FakeDocument(uri, provider.provideTextDocumentContent(uri) as string);
      },
    },

    commands: {
      registerCommand: (id: string, handler: (...args: any[]) => unknown) => {
        if (control.commands.has(id)) throw new Error(`command registered twice: ${id}`);
        control.commands.set(id, handler);
        return { dispose: () => void control.commands.delete(id) };
      },
      executeCommand: (id: string, ...args: unknown[]) => {
        const handler = control.commands.get(id);
        return handler !== undefined ? Promise.resolve(handler(...args)) : builtin(id, args);
      },
    },

    languages: {
      registerCodeLensProvider: (_selector: unknown, provider: unknown) => {
        control.codeLensProviders.push(provider);
        return disposable;
      },
    },

    tasks: {
      onDidEndTaskProcess: control.onDidEndTaskProcess.event,
      onDidEndTask: control.onDidEndTask.event,
      executeTask: (task: Task) => {
        control.tasks.push(task);
        const execution = { task };
        setImmediate(() => {
          const exitCode = control.taskExitCode(task);
          if (exitCode !== undefined) control.onDidEndTaskProcess.fire({ execution, exitCode });
          control.onDidEndTask.fire({ execution });
        });
        return Promise.resolve(execution);
      },
    },
  };

  return { api, control };
}
