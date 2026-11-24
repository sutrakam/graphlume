// One place that turns "ask GraphLume this" into a process and an outcome.
// It has no VS Code dependency, so it can be driven by a fake runner in tests
// and by the real binary from a script.

import type { ToolName } from './args';
import { statusArgv, toolCallArgv } from './args';
import type { ToolOutcome } from './result';
import { interpretToolRun } from './result';
import type { Runner } from './runner';
import type { IndexStatus } from './status';
import { parseStatus, StatusError } from './status';

/** How long one tool call may take before it is stopped. */
export const TOOL_TIMEOUT_MS = 20_000;
/** How long `status` may take. It only opens the database. */
export const STATUS_TIMEOUT_MS = 10_000;

export interface CallOptions {
  readonly signal?: AbortSignal;
  readonly timeoutMs?: number;
}

export interface ToolCall {
  readonly outcome: ToolOutcome;
  /** The exact arguments the binary was started with, for the full report. */
  readonly argv: readonly string[];
}

export class GraphLumeClient {
  constructor(
    private readonly runner: Runner,
    private readonly binary: () => string,
  ) {}

  /**
   * Calls one tool. Rejects only when the binary could not be run, timed out
   * or was cancelled; everything the binary itself reports comes back as an
   * outcome.
   */
  async callTool(
    projectRoot: string,
    tool: ToolName,
    args: Readonly<Record<string, unknown>>,
    options: CallOptions = {},
  ): Promise<ToolCall> {
    const argv = toolCallArgv(projectRoot, tool, args);
    const run = await this.runner({
      binary: this.binary(),
      args: argv,
      cwd: projectRoot,
      timeoutMs: options.timeoutMs ?? TOOL_TIMEOUT_MS,
      ...(options.signal !== undefined ? { signal: options.signal } : {}),
    });
    return { outcome: interpretToolRun(run), argv };
  }

  /** Reads the index status. `status` works, and creates nothing, when there is no index. */
  async status(projectRoot: string, options: CallOptions = {}): Promise<IndexStatus> {
    const run = await this.runner({
      binary: this.binary(),
      args: statusArgv(projectRoot),
      cwd: projectRoot,
      timeoutMs: options.timeoutMs ?? STATUS_TIMEOUT_MS,
      ...(options.signal !== undefined ? { signal: options.signal } : {}),
    });
    if (run.stdout.trim() === '') {
      const stderr = run.stderr.trim().replace(/^error:\s*/iu, '');
      throw new StatusError(stderr !== '' ? stderr : `graphlume status exited with status ${String(run.code)} and printed nothing.`);
    }
    return parseStatus(run.stdout);
  }
}
