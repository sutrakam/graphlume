// GraphLume for VS Code.
//
// Everything this extension shows comes from running the installed
// `graphlume` binary against the index in the workspace's `.graphlume/`
// directory. It opens no network connection and sends nothing anywhere.

import * as vscode from 'vscode';

import type { QuestionKind } from './core/args';
import { checkSymbolName, findSymbolArguments, indexStepsFor, QUESTION_TITLE, QUESTION_TOOL, questionArguments } from './core/args';
import { GraphLumeClient } from './core/client';
import type { UserMessage } from './core/messages';
import { messageForAmbiguous, messageForError, messageForOutcome } from './core/messages';
import { toRepoRelative } from './core/paths';
import { buildResultModel, fullReport } from './core/present';
import type { ToolAnswer, ToolOutcome } from './core/result';
import { isAmbiguous } from './core/result';
import { runBinary } from './core/runner';
import type { Definition, DefinitionHint } from './core/target';
import { candidatesFrom, chooseDefinition, definitionsFrom, describeDefinition, targetFor, targetWarnings } from './core/target';
import type { LensRequest } from './codeLens';
import { CallerLensProvider } from './codeLens';
import { affectsGraphLume, binaryPath, currentProjectRoot, minConfidence, projectRootFor } from './config';
import { runIndexStep } from './indexTask';
import { ResultsView } from './resultsView';
import { StatusController } from './statusBar';

/** How long to wait for the language server's go-to-definition before going without it. */
const DEFINITION_HINT_TIMEOUT_MS = 1_500;

function isLensRequest(value: unknown): value is LensRequest {
  if (typeof value !== 'object' || value === null) {
    return false;
  }
  const v = value as Record<string, unknown>;
  return typeof v['uri'] === 'string' && typeof v['name'] === 'string' && typeof v['line'] === 'number';
}

/** What a question is about: a name, and where in which file it was pointed at. */
interface Subject {
  readonly name: string;
  readonly uri: vscode.Uri;
  readonly position: vscode.Position;
  /** True when the position is known to be the definition itself (a CodeLens). */
  readonly atDefinition: boolean;
}

function subjectFromEditor(): Subject | string {
  const editor = vscode.window.activeTextEditor;
  if (editor === undefined) {
    return 'Open a file and put the cursor on a function, method or type first.';
  }
  const document = editor.document;
  if (document.uri.scheme !== 'file') {
    return 'GraphLume answers questions about files on disk. This editor is not one.';
  }
  const selection = editor.selection;
  let range: vscode.Range | undefined;
  if (selection.isEmpty) {
    range = document.getWordRangeAtPosition(selection.active);
  } else if (selection.isSingleLine) {
    range = selection;
  }
  const check = checkSymbolName(range !== undefined ? document.getText(range) : '');
  if (!check.ok) {
    return check.reason;
  }
  return { name: check.name, uri: document.uri, position: (range ?? selection).start, atDefinition: false };
}

function subjectFromLens(request: LensRequest): Subject | string {
  const check = checkSymbolName(request.name);
  if (!check.ok) {
    return check.reason;
  }
  let uri: vscode.Uri;
  try {
    uri = vscode.Uri.parse(request.uri, true);
  } catch {
    return 'That CodeLens no longer points at a file.';
  }
  return { name: check.name, uri, position: new vscode.Position(Math.max(0, Math.floor(request.line)), 0), atDefinition: true };
}

function withTimeout<T>(work: Thenable<T>, ms: number): Promise<T | undefined> {
  return new Promise<T | undefined>((resolve) => {
    const timer = setTimeout(() => resolve(undefined), ms);
    work.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      () => {
        clearTimeout(timer);
        resolve(undefined);
      },
    );
  });
}

/**
 * Where the symbol is defined, as far as the editor knows. The language
 * server's go-to-definition is asked first because it names the real
 * definition; without one, the cursor position is all there is.
 */
async function definitionHint(subject: Subject, root: string): Promise<DefinitionHint> {
  const here = toRepoRelative(root, subject.uri.fsPath);
  if (subject.atDefinition) {
    return here !== undefined
      ? { source: 'definition', path: here, line: subject.position.line + 1 }
      : { source: 'definition', outsideProject: subject.uri.fsPath };
  }
  const locations = await withTimeout(
    vscode.commands.executeCommand<(vscode.Location | vscode.LocationLink)[] | undefined>(
      'vscode.executeDefinitionProvider',
      subject.uri,
      subject.position,
    ),
    DEFINITION_HINT_TIMEOUT_MS,
  );
  const first = locations?.[0];
  if (first !== undefined) {
    const uri = 'targetUri' in first ? first.targetUri : first.uri;
    const range = 'targetUri' in first ? (first.targetSelectionRange ?? first.targetRange) : first.range;
    if (uri.scheme === 'file') {
      const relative = toRepoRelative(root, uri.fsPath);
      return relative !== undefined
        ? { source: 'definition', path: relative, line: range.start.line + 1 }
        : { source: 'definition', outsideProject: uri.fsPath };
    }
    return { source: 'definition', outsideProject: uri.toString(true) };
  }
  return here !== undefined ? { source: 'cursor', path: here, line: subject.position.line + 1 } : { source: 'none' };
}

export function activate(context: vscode.ExtensionContext): void {
  const client = new GraphLumeClient(runBinary, binaryPath);
  const status = new StatusController(client);
  const results = new ResultsView();
  const lenses = new CallerLensProvider(client, status);

  // Aborted when the extension is deactivated, so no process outlives it.
  const lifetime = new AbortController();
  // The question being answered. Asking another one stops it: the view only
  // ever shows one answer, and it should be the one asked for last.
  let asking: AbortController | undefined;

  const notify = async (message: UserMessage): Promise<void> => {
    const label =
      message.remedy === 'open-settings'
        ? 'Open Settings'
        : message.remedy === 'index'
          ? 'Index Workspace'
          : message.remedy === 'show-report'
            ? 'Show Report'
            : undefined;
    const actions = label !== undefined ? [label] : [];
    const show =
      message.severity === 'error'
        ? vscode.window.showErrorMessage
        : message.severity === 'warning'
          ? vscode.window.showWarningMessage
          : vscode.window.showInformationMessage;
    const picked = await show(message.text, ...actions);
    if (picked === undefined) {
      return;
    }
    if (message.remedy === 'open-settings') {
      await vscode.commands.executeCommand('workbench.action.openSettings', 'graphlume.path');
    } else if (message.remedy === 'index') {
      await vscode.commands.executeCommand('graphlume.indexWorkspace');
    } else if (message.remedy === 'show-report') {
      await results.showFullReport();
    }
  };

  /** Reports a run that produced no usable answer, in the view and as a notification. */
  const reportOutcome = (title: string, outcome: Exclude<ToolOutcome, { kind: 'answer' }>): void => {
    const message = messageForOutcome(outcome);
    const text = 'answer' in outcome && outcome.answer !== undefined ? outcome.answer.text : undefined;
    results.showProblem(title, message.text, text);
    if (outcome.kind === 'no-index') {
      void status.refresh();
    }
    void notify(message);
  };

  /**
   * Puts several definitions of one name to the user. 'tool-listed' is the
   * case where a question was asked and the tool listed these instead of
   * answering, because the name, path and line did not come down to one.
   */
  const pickDefinition = async (
    name: string,
    candidates: readonly Definition[],
    reason: 'ambiguous' | 'hint-mismatch' | 'tool-listed',
    hint: DefinitionHint,
    listWasCut: boolean,
  ): Promise<Definition | undefined> => {
    const where = hint.outsideProject ?? hint.path;
    const title =
      reason === 'hint-mismatch'
        ? `The editor resolves "${name}" to ${where ?? 'a definition'} which the index does not list under that name. ` +
          'These indexed definitions only share the name.'
        : reason === 'tool-listed'
          ? `"${name}" matches several definitions and GraphLume answered for none of them. It listed ${candidates.length} to choose from.`
          : `${candidates.length} indexed definitions are named "${name}".`;
    const picked = await vscode.window.showQuickPick(
      candidates.map((definition) => ({
        label: definition.line > 0 ? `${definition.path}:${definition.line}` : definition.path,
        description: [definition.kind, definition.detail ?? ''].filter((s) => s !== '').join(', '),
        definition,
      })),
      {
        title,
        placeHolder: listWasCut ? 'Choose a definition (GraphLume cut this list short)' : 'Choose the definition to ask about',
        matchOnDescription: true,
      },
    );
    return picked?.definition;
  };

  const ask = async (kind: QuestionKind, argument: unknown): Promise<void> => {
    const subject = isLensRequest(argument) ? subjectFromLens(argument) : subjectFromEditor();
    if (typeof subject === 'string') {
      void vscode.window.showInformationMessage(`GraphLume: ${subject}`);
      return;
    }
    const root = projectRootFor(subject.uri);
    if (root === undefined) {
      void vscode.window.showInformationMessage(
        'GraphLume: this file is not inside an open folder, so there is no index to ask. Open the repository folder first.',
      );
      return;
    }
    const title = `${QUESTION_TITLE[kind]} ${subject.name}`;
    const floor = minConfidence(subject.uri);

    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `GraphLume: ${title}`, cancellable: true },
      async (_progress, token) => {
        asking?.abort();
        const controller = new AbortController();
        asking = controller;
        const cancel = token.onCancellationRequested(() => controller.abort());
        const signal = AbortSignal.any([controller.signal, lifetime.signal]);
        try {
          const hint = await definitionHint(subject, root);
          if (signal.aborted) {
            return;
          }

          // Step 1: which definitions does the index hold under this name?
          const lookup = await client.callTool(root, 'symbol', findSymbolArguments(subject.name), { signal });
          if (lookup.outcome.kind !== 'answer') {
            reportOutcome(title, lookup.outcome);
            return;
          }
          const found: ToolAnswer = lookup.outcome.answer;
          status.noteProvenance(root, found.provenance);
          const definitions = definitionsFrom(found).filter((d) => d.name === subject.name);
          const choice = chooseDefinition(definitions, hint);

          let chosen: Definition;
          if (choice.kind === 'none') {
            // Not in the index. Show the tool's own answer, which says that
            // absence here is not proof the symbol does not exist.
            const notes: string[] = [];
            if (hint.outsideProject !== undefined) {
              notes.push(
                `The editor resolves "${subject.name}" to ${hint.outsideProject}, outside this project. GraphLume indexes this repository only.`,
              );
            }
            const model = buildResultModel(found, { kind, symbol: subject.name, extensionWarnings: notes });
            results.show(model, root, fullReport(model, found, lookup.argv));
            return;
          } else if (choice.kind === 'ask') {
            const picked = await pickDefinition(subject.name, choice.candidates, choice.reason, hint, found.truncated);
            if (picked === undefined || signal.aborted) {
              return;
            }
            chosen = picked;
          } else {
            chosen = choice.definition;
          }

          // Step 2: the question itself, about that one definition.
          let namesakes: readonly Definition[] = definitions;
          let call = await client.callTool(root, QUESTION_TOOL[kind], questionArguments(kind, targetFor(chosen), floor), { signal });

          // The tool answers for one definition or for none. When the path
          // and line did not come down to one (two definitions start on one
          // line, the index reported no line, or it was rebuilt in between)
          // it lists the candidates instead. Those are definitions, not
          // results: they go to the user as a choice and never into the view.
          if (call.outcome.kind === 'answer' && isAmbiguous(call.outcome.answer)) {
            const listed = call.outcome.answer;
            status.noteProvenance(root, listed.provenance);
            const candidates = candidatesFrom(listed);
            const picked =
              candidates.length > 0 ? await pickDefinition(subject.name, candidates, 'tool-listed', hint, listed.truncated) : undefined;
            if (signal.aborted) {
              return;
            }
            if (picked === undefined) {
              const message = messageForAmbiguous(subject.name, listed);
              results.showProblem(title, message.text, listed.text);
              if (candidates.length === 0) {
                // Nothing could be offered, so the message is the only account of what happened.
                void notify(message);
              }
              return;
            }
            chosen = picked;
            namesakes = candidates;
            call = await client.callTool(root, QUESTION_TOOL[kind], questionArguments(kind, targetFor(chosen), floor), { signal });
          }

          if (call.outcome.kind !== 'answer') {
            reportOutcome(title, call.outcome);
            return;
          }
          const answer = call.outcome.answer;
          status.noteProvenance(root, answer.provenance);
          if (isAmbiguous(answer)) {
            // Asked with a path and line the tool itself listed, and it still
            // chose none. Asking a third time would not change that.
            const message = messageForAmbiguous(subject.name, answer);
            results.showProblem(title, message.text, answer.text);
            void notify(message);
            return;
          }
          const model = buildResultModel(answer, {
            kind,
            symbol: subject.name,
            extensionWarnings: targetWarnings(chosen, namesakes, answer),
            askedAbout: describeDefinition(chosen),
          });
          results.show(model, root, fullReport(model, answer, call.argv));
        } catch (err) {
          const message = messageForError(err);
          if (message !== undefined) {
            results.showProblem(title, message.text, undefined);
            void status.refresh();
            void notify(message);
          }
        } finally {
          cancel.dispose();
          if (asking === controller) {
            asking = undefined;
          }
        }
      },
    );
  };

  let indexing = false;
  const indexWorkspace = async (): Promise<void> => {
    if (indexing) {
      void vscode.window.showInformationMessage('GraphLume: an index run started from this window is still going. See the terminal.');
      return;
    }
    const root = currentProjectRoot();
    if (root === undefined) {
      void vscode.window.showInformationMessage('GraphLume: open the repository folder first, then index it.');
      return;
    }
    indexing = true;
    try {
      let indexStatus: string;
      try {
        indexStatus = (await client.status(root, { signal: lifetime.signal })).indexStatus;
      } catch (err) {
        const message = messageForError(err);
        if (message !== undefined) {
          void status.refresh();
          void notify(message);
        }
        return;
      }
      const binary = binaryPath();
      for (const step of indexStepsFor(indexStatus)) {
        const code = await runIndexStep(binary, root, step);
        if (code !== 0) {
          void vscode.window.showWarningMessage(
            code === undefined
              ? `GraphLume: "graphlume ${step}" did not run. See the terminal for the reason.`
              : `GraphLume: "graphlume ${step}" exited with status ${code}. The later steps were not run. See the terminal.`,
          );
          return;
        }
      }
    } catch (err) {
      void vscode.window.showErrorMessage(`GraphLume: could not start the index task. ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      indexing = false;
      void status.refresh();
    }
  };

  const showStatus = async (): Promise<void> => {
    const view = status.presentation();
    interface Action extends vscode.QuickPickItem {
      readonly run?: () => Thenable<unknown>;
    }
    const items: Action[] = [
      {
        label: '$(play) Index this workspace in a terminal',
        detail: 'Runs graphlume index, then graphlume resolve (and graphlume init first when there is no index).',
        run: () => vscode.commands.executeCommand('graphlume.indexWorkspace'),
      },
      { label: '$(refresh) Refresh index status', run: () => status.refresh() },
      {
        label: '$(gear) Open GraphLume settings',
        run: () => vscode.commands.executeCommand('workbench.action.openSettings', 'graphlume'),
      },
      { label: 'Details', kind: vscode.QuickPickItemKind.Separator },
      ...view.detail.map((line): Action => ({ label: line })),
    ];
    const picked = await vscode.window.showQuickPick(items, {
      title: view.text + (status.projectRoot !== undefined ? ` (${status.projectRoot})` : ''),
      placeHolder: view.suggestIndex ? 'The index needs building or refreshing' : 'Choose an action',
    });
    await picked?.run?.();
  };

  context.subscriptions.push(
    { dispose: () => lifetime.abort() },
    status,
    results,
    lenses,
    vscode.commands.registerCommand('graphlume.showCallers', (argument?: unknown) => ask('callers', argument)),
    vscode.commands.registerCommand('graphlume.showImpact', (argument?: unknown) => ask('impact', argument)),
    vscode.commands.registerCommand('graphlume.showTests', (argument?: unknown) => ask('tests', argument)),
    vscode.commands.registerCommand('graphlume.showImplementations', (argument?: unknown) => ask('implementations', argument)),
    vscode.commands.registerCommand('graphlume.indexWorkspace', indexWorkspace),
    vscode.commands.registerCommand('graphlume.refreshStatus', () => status.refresh()),
    vscode.commands.registerCommand('graphlume.showStatus', showStatus),
    vscode.commands.registerCommand('graphlume.showFullReport', () => results.showFullReport()),
    vscode.commands.registerCommand('graphlume.openResult', (row: Parameters<ResultsView['open']>[0]) => results.open(row)),
    vscode.languages.registerCodeLensProvider({ scheme: 'file' }, lenses),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (affectsGraphLume(event)) {
        lenses.refresh();
        void status.refresh();
      }
    }),
  );

  void status.refresh();
}

export function deactivate(): void {
  // Everything is disposed through the extension context's subscriptions.
}
