import * as vscode from 'vscode';

import type { IndexStep } from './core/args';
import { indexStepArgv } from './core/args';

const TASK_TYPE = 'graphlume';
const TASK_SOURCE = 'GraphLume';

/**
 * Runs one index-building step as a VS Code task, so its output is in a
 * terminal the user can read and stop.
 *
 * The task uses ProcessExecution: the binary is started directly with an
 * argument array, never through a shell, so the project path and the binary
 * path are not interpreted by one.
 *
 * Resolves with the exit status, or undefined when the process did not start.
 */
export function runIndexStep(binary: string, projectRoot: string, step: IndexStep): Promise<number | undefined> {
  const folder = vscode.workspace.getWorkspaceFolder(vscode.Uri.file(projectRoot));
  const task = new vscode.Task(
    { type: TASK_TYPE, step },
    folder ?? vscode.TaskScope.Workspace,
    `graphlume ${step}`,
    TASK_SOURCE,
    new vscode.ProcessExecution(binary, indexStepArgv(projectRoot, step), { cwd: projectRoot }),
    [],
  );
  task.presentationOptions = {
    reveal: vscode.TaskRevealKind.Always,
    panel: vscode.TaskPanelKind.Shared,
    focus: false,
    echo: true,
    clear: false,
    showReuseMessage: false,
  };

  return new Promise<number | undefined>((resolve, reject) => {
    let exitCode: number | undefined;
    const isThis = (execution: vscode.TaskExecution): boolean =>
      execution.task.definition.type === TASK_TYPE &&
      execution.task.definition['step'] === step &&
      execution.task.name === task.name;

    const processEnded = vscode.tasks.onDidEndTaskProcess((event) => {
      if (isThis(event.execution)) {
        exitCode = event.exitCode;
      }
    });
    const taskEnded = vscode.tasks.onDidEndTask((event) => {
      if (isThis(event.execution)) {
        processEnded.dispose();
        taskEnded.dispose();
        resolve(exitCode);
      }
    });
    vscode.tasks.executeTask(task).then(undefined, (err: unknown) => {
      processEnded.dispose();
      taskEnded.dispose();
      reject(err instanceof Error ? err : new Error(String(err)));
    });
  });
}
