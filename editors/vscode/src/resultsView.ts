import * as vscode from 'vscode';

import { describeConfidence } from './core/confidence';
import { findNameLine, resolveInsideRoot } from './core/paths';
import type { LimitRow, ResultModel, ResultRow, Section } from './core/present';
import { limitLine } from './core/present';

export const RESULTS_VIEW_ID = 'graphlume.results';
export const REPORT_SCHEME = 'graphlume-report';

type Node = Section | LimitRow | ResultRow;

function isSection(node: Node): node is Section {
  return 'rows' in node;
}

function tierIcon(confidence: string): vscode.ThemeIcon {
  switch (confidence) {
    case 'exact':
      return new vscode.ThemeIcon('verified-filled', new vscode.ThemeColor('testing.iconPassed'));
    case 'high':
      return new vscode.ThemeIcon('pass', new vscode.ThemeColor('testing.iconPassed'));
    case 'medium':
      return new vscode.ThemeIcon('question', new vscode.ThemeColor('list.warningForeground'));
    case 'low':
      return new vscode.ThemeIcon('warning', new vscode.ThemeColor('list.warningForeground'));
    default:
      return new vscode.ThemeIcon('circle-outline');
  }
}

/** Serves the last full report as a read-only document. */
class ReportProvider implements vscode.TextDocumentContentProvider {
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  private text = '';
  readonly onDidChange = this.changed.event;
  readonly uri = vscode.Uri.from({ scheme: REPORT_SCHEME, path: '/GraphLume report.txt' });

  set(text: string): void {
    this.text = text;
    this.changed.fire(this.uri);
  }

  get hasReport(): boolean {
    return this.text !== '';
  }

  provideTextDocumentContent(): string {
    return this.text;
  }

  dispose(): void {
    this.changed.dispose();
  }
}

/**
 * The results view: a tree with the tool's statements about its own answer
 * first, then the result rows grouped by kind. Each result row states its
 * confidence tier in text and opens the file at the reported line.
 */
export class ResultsView implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<Node | undefined | void>();
  private readonly view: vscode.TreeView<Node>;
  private readonly report = new ReportProvider();
  private readonly disposables: vscode.Disposable[] = [];
  private model: ResultModel | undefined;
  private root: string | undefined;

  readonly onDidChangeTreeData = this.changed.event;

  constructor() {
    this.view = vscode.window.createTreeView(RESULTS_VIEW_ID, { treeDataProvider: this, showCollapseAll: true });
    this.disposables.push(
      this.view,
      this.changed,
      this.report,
      vscode.workspace.registerTextDocumentContentProvider(REPORT_SCHEME, this.report),
    );
  }

  dispose(): void {
    for (const d of this.disposables.splice(0)) {
      d.dispose();
    }
  }

  /** Shows an answer. `reportText` is what "Show the full tool report" opens. */
  show(model: ResultModel, projectRoot: string, reportText: string): void {
    this.model = model;
    this.root = projectRoot;
    this.report.set(reportText);
    this.view.title = model.title;
    // The count is the tool's own when it gave one, and a doubtful answer
    // says so here, beside the title, before any row is read.
    this.view.description = model.status;
    this.view.message = model.summary;
    this.changed.fire();
    void this.reveal();
  }

  /** Shows a failure in place of results, so it stays readable after the notification goes. */
  showProblem(title: string, text: string, reportText: string | undefined): void {
    this.model = undefined;
    this.root = undefined;
    this.report.set(reportText ?? '');
    this.view.title = title;
    this.view.description = '';
    this.view.message = text;
    this.changed.fire();
    void this.reveal();
  }

  private async reveal(): Promise<void> {
    try {
      await vscode.commands.executeCommand(`${RESULTS_VIEW_ID}.focus`, { preserveFocus: true });
    } catch {
      // The view exists but could not be focused; the results are still in it.
    }
  }

  getChildren(node?: Node): Node[] {
    if (node === undefined) {
      return this.model !== undefined ? [...this.model.sections] : [];
    }
    return isSection(node) ? [...node.rows] : [];
  }

  getTreeItem(node: Node): vscode.TreeItem {
    if (isSection(node)) {
      const item = new vscode.TreeItem(node.title, vscode.TreeItemCollapsibleState.Expanded);
      if (node.warning) {
        item.iconPath = new vscode.ThemeIcon('warning', new vscode.ThemeColor('list.warningForeground'));
      }
      return item;
    }
    if (node.type === 'limit') {
      const item = new vscode.TreeItem(limitLine(node), vscode.TreeItemCollapsibleState.None);
      item.iconPath = node.warning
        ? new vscode.ThemeIcon('warning', new vscode.ThemeColor('list.warningForeground'))
        : new vscode.ThemeIcon('info');
      item.tooltip =
        node.author === 'extension'
          ? `${node.text}\n\nWritten by the GraphLume extension, not by the graphlume tool.`
          : `${node.text}\n\nGraphLume's own statement. Click to open the full report.`;
      item.command = { command: 'graphlume.showFullReport', title: 'Show the full tool report' };
      return item;
    }
    const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.None);
    item.description = node.description;
    item.iconPath = tierIcon(node.confidence);
    item.tooltip = node.tooltip;
    item.accessibilityInformation = {
      label: `${node.label}, ${describeConfidence(node.confidence !== '' ? node.confidence : undefined)} ${node.path ?? ''}${
        node.line !== undefined ? ` line ${node.line}` : ''
      }`,
    };
    if (node.path !== undefined) {
      item.command = { command: 'graphlume.openResult', title: 'Open', arguments: [node] };
    }
    return item;
  }

  /** Opens the file a result row points at, at its line. */
  async open(row: ResultRow): Promise<void> {
    if (this.root === undefined || row.path === undefined) {
      return;
    }
    const absolute = resolveInsideRoot(this.root, row.path);
    if (absolute === undefined) {
      void vscode.window.showWarningMessage(
        `GraphLume: "${row.path}" is not a path inside this project, so it was not opened.`,
      );
      return;
    }
    let document: vscode.TextDocument;
    try {
      document = await vscode.workspace.openTextDocument(vscode.Uri.file(absolute));
    } catch {
      void vscode.window.showWarningMessage(
        `GraphLume: ${row.path} could not be opened. It may have been moved or deleted since the index was built.`,
      );
      return;
    }
    let line: number;
    if (row.line !== undefined) {
      line = row.line - 1;
    } else {
      // The tool gave no line: find the name in the file as it is now.
      line = (row.name !== undefined ? findNameLine(document.getText(), row.name) : undefined) ?? 0;
    }
    line = Math.max(0, Math.min(line, document.lineCount - 1));
    const text = document.lineAt(line).text;
    const column = row.name !== undefined ? Math.max(0, text.indexOf(row.name)) : 0;
    const end = row.name !== undefined && text.indexOf(row.name) >= 0 ? column + row.name.length : column;
    const selection = new vscode.Range(line, column, line, end);
    await vscode.window.showTextDocument(document, { selection, preview: true });
  }

  /** Opens the last answer's full text in a read-only editor. */
  async showFullReport(): Promise<void> {
    if (!this.report.hasReport) {
      void vscode.window.showInformationMessage('GraphLume: there is no report yet. Run a GraphLume command on a symbol first.');
      return;
    }
    const document = await vscode.workspace.openTextDocument(this.report.uri);
    await vscode.window.showTextDocument(document, { preview: true, viewColumn: vscode.ViewColumn.Beside });
  }
}
