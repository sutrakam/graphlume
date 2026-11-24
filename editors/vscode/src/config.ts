import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import * as vscode from 'vscode';

import { expandBinaryPath } from './core/args';
import type { Confidence } from './core/confidence';
import { normalizeMinConfidence } from './core/confidence';
import { findProjectRoot } from './core/paths';

const SECTION = 'graphlume';

/** The binary to run. A machine-scoped setting: a workspace cannot change it. */
export function binaryPath(): string {
  const configured = vscode.workspace.getConfiguration(SECTION).get<string>('path', 'graphlume');
  return expandBinaryPath(typeof configured === 'string' ? configured : 'graphlume', os.homedir());
}

export function codeLensEnabled(scope?: vscode.Uri): boolean {
  return vscode.workspace.getConfiguration(SECTION, scope).get<boolean>('codeLens.enabled', false) === true;
}

export function minConfidence(scope?: vscode.Uri): Confidence {
  return normalizeMinConfidence(vscode.workspace.getConfiguration(SECTION, scope).get<string>('minConfidence'));
}

export function affectsGraphLume(event: vscode.ConfigurationChangeEvent): boolean {
  return event.affectsConfiguration(SECTION);
}

function hasIndexDir(dir: string): boolean {
  try {
    return fs.statSync(path.join(dir, '.graphlume')).isDirectory();
  } catch {
    return false;
  }
}

/**
 * The directory to point GraphLume at for a file: the nearest folder, up to
 * the workspace folder, that holds `.graphlume/`. Undefined when the file
 * is not on disk inside an open folder.
 */
export function projectRootFor(uri: vscode.Uri | undefined): string | undefined {
  if (uri !== undefined && uri.scheme === 'file') {
    const folder = vscode.workspace.getWorkspaceFolder(uri);
    if (folder !== undefined && folder.uri.scheme === 'file') {
      return findProjectRoot(path.dirname(uri.fsPath), folder.uri.fsPath, hasIndexDir);
    }
    return undefined;
  }
  return undefined;
}

/** The project root for the active editor, else the first folder on disk. */
export function currentProjectRoot(): string | undefined {
  const active = projectRootFor(vscode.window.activeTextEditor?.document.uri);
  if (active !== undefined) {
    return active;
  }
  const first = vscode.workspace.workspaceFolders?.find((f) => f.uri.scheme === 'file');
  return first?.uri.fsPath;
}
