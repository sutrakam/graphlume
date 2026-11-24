// Path handling between the editor (absolute, platform separators) and the
// index (repository-relative, forward slashes).
//
// Paths in a tool result come from the index, which was built from a
// repository the user may not have written. They are treated as data: a row
// is only made clickable when its path resolves to somewhere inside the
// project root.

import * as nodePath from 'node:path';

export interface PathApi {
  readonly sep: string;
  isAbsolute(p: string): boolean;
  relative(from: string, to: string): string;
  resolve(...segments: string[]): string;
  dirname(p: string): string;
}

/**
 * The repository-relative path of `file`, with forward slashes, or undefined
 * when the file is not inside `root`.
 */
export function toRepoRelative(root: string, file: string, api: PathApi = nodePath): string | undefined {
  const rel = api.relative(root, file);
  if (rel === '' || api.isAbsolute(rel) || rel === '..' || rel.startsWith('..' + api.sep)) {
    return undefined;
  }
  return api.sep === '/' ? rel : rel.split(api.sep).join('/');
}

/**
 * The absolute path for a repository-relative path from a tool result, or
 * undefined when it is absolute, empty, or would land outside `root`.
 */
export function resolveInsideRoot(root: string, relative: string, api: PathApi = nodePath): string | undefined {
  if (relative === '' || relative.includes('\0')) {
    return undefined;
  }
  // A leading slash or a drive letter is never a repository-relative path,
  // on any platform.
  if (api.isAbsolute(relative) || relative.startsWith('/') || relative.startsWith('\\') || /^[A-Za-z]:/u.test(relative)) {
    return undefined;
  }
  const absolute = api.resolve(root, relative);
  const back = api.relative(root, absolute);
  if (back === '' || api.isAbsolute(back) || back === '..' || back.startsWith('..' + api.sep)) {
    return undefined;
  }
  return absolute;
}

/**
 * The directory GraphLume should be pointed at for a file: the nearest
 * directory, from the file's own up to the workspace folder, that holds a
 * `.graphlume` directory. Falls back to the workspace folder, which is
 * where an index would be created.
 */
export function findProjectRoot(
  fileDir: string,
  workspaceRoot: string,
  hasIndexDir: (dir: string) => boolean,
  api: PathApi = nodePath,
): string {
  const inside = toRepoRelative(workspaceRoot, fileDir, api) !== undefined || api.relative(workspaceRoot, fileDir) === '';
  if (!inside) {
    return workspaceRoot;
  }
  let dir = fileDir;
  // Bounded: a path has a finite number of parents, and the loop stops at the workspace root.
  for (let i = 0; i < 256; i++) {
    if (hasIndexDir(dir)) {
      return dir;
    }
    if (api.relative(workspaceRoot, dir) === '') {
      break;
    }
    const parent = api.dirname(dir);
    if (parent === dir) {
      break;
    }
    dir = parent;
  }
  return workspaceRoot;
}

/**
 * The 0-based line where `name` most plausibly is defined in `text`, for a
 * row the tool reported without a line. Prefers a line that looks like a
 * declaration, then falls back to the first whole-word occurrence. Returns
 * undefined when the name does not appear.
 */
export function findNameLine(text: string, name: string): number | undefined {
  if (name === '') {
    return undefined;
  }
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&');
  const word = new RegExp(`(?<![\\p{L}\\p{N}_$])${escaped}(?![\\p{L}\\p{N}_$])`, 'u');
  // A declaration keyword, an optional Go receiver in parentheses, then the
  // name with no other "(", "=", ";" or "{" in between. That keeps a
  // parameter or a call that merely mentions the name from matching.
  const declaration = new RegExp(
    '\\b(?:func|function|def|fn|class|interface|struct|enum|type|trait|record|object|void|public|private|protected|static|async|const|let|var)\\b' +
      '(?:\\s*\\([^)]*\\))?[^=;{(]*?' +
      `(?<![\\p{L}\\p{N}_$])${escaped}(?![\\p{L}\\p{N}_$])`,
    'u',
  );
  const lines = text.split(/\r?\n/u);
  let first: number | undefined;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] as string;
    if (!word.test(line)) {
      continue;
    }
    if (declaration.test(line)) {
      return i;
    }
    first ??= i;
  }
  return first;
}
