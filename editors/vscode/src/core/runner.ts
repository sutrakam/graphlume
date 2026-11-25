// Runs the graphlume binary.
//
// The binary is started directly with an argument array: no shell is ever
// involved, so nothing in an argument is interpreted by one. Every run has a
// timeout and an abort signal, and its output is capped so a misbehaving
// binary cannot exhaust the editor's memory.

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';

export interface RunRequest {
  /** A path, or a bare command name looked up on PATH by the operating system. */
  readonly binary: string;
  readonly args: readonly string[];
  readonly cwd: string;
  readonly timeoutMs: number;
  readonly signal?: AbortSignal;
  /** Combined stdout and stderr limit in bytes. Default 8 MiB. */
  readonly maxOutputBytes?: number;
}

export interface RunResult {
  readonly code: number | null;
  readonly stdout: string;
  readonly stderr: string;
}

export type Runner = (request: RunRequest) => Promise<RunResult>;

/** The binary does not exist, or is not on PATH. */
export class BinaryNotFoundError extends Error {
  constructor(readonly binary: string) {
    super(`The graphlume binary was not found (tried "${binary}").`);
    this.name = 'BinaryNotFoundError';
  }
}

/** The binary exists but could not be started. */
export class BinaryNotRunnableError extends Error {
  constructor(
    readonly binary: string,
    readonly reason: string,
  ) {
    super(`"${binary}" could not be started: ${reason}`);
    this.name = 'BinaryNotRunnableError';
  }
}

export class RunTimeoutError extends Error {
  constructor(readonly timeoutMs: number) {
    super(`graphlume did not answer within ${Math.round(timeoutMs / 1000)} seconds and was stopped.`);
    this.name = 'RunTimeoutError';
  }
}

export class RunCancelledError extends Error {
  constructor() {
    super('Cancelled.');
    this.name = 'RunCancelledError';
  }
}

export class OutputTooLargeError extends Error {
  constructor(readonly limitBytes: number) {
    super(`graphlume printed more than ${limitBytes} bytes and was stopped.`);
    this.name = 'OutputTooLargeError';
  }
}

const DEFAULT_MAX_OUTPUT_BYTES = 8 * 1024 * 1024;

/** Starts the binary, waits for it to exit, and returns what it printed. */
export const runBinary: Runner = (request) => {
  const limit = request.maxOutputBytes ?? DEFAULT_MAX_OUTPUT_BYTES;
  return new Promise<RunResult>((resolve, reject) => {
    if (request.signal?.aborted === true) {
      reject(new RunCancelledError());
      return;
    }

    let child;
    try {
      child = spawn(request.binary, [...request.args], {
        cwd: request.cwd,
        shell: false,
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
      });
    } catch (err) {
      reject(new BinaryNotRunnableError(request.binary, err instanceof Error ? err.message : String(err)));
      return;
    }

    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    let bytes = 0;
    let settled = false;

    const finish = (action: () => void): void => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timer);
      request.signal?.removeEventListener('abort', onAbort);
      action();
    };

    const stop = (): void => {
      // SIGKILL: the process only reads a local database, so there is nothing
      // for it to clean up, and a polite signal could be ignored.
      child.kill('SIGKILL');
    };

    const onAbort = (): void => {
      stop();
      finish(() => reject(new RunCancelledError()));
    };

    const timer = setTimeout(() => {
      stop();
      finish(() => reject(new RunTimeoutError(request.timeoutMs)));
    }, request.timeoutMs);

    request.signal?.addEventListener('abort', onAbort, { once: true });

    const collect = (into: Buffer[]) => (chunk: Buffer) => {
      bytes += chunk.length;
      if (bytes > limit) {
        stop();
        finish(() => reject(new OutputTooLargeError(limit)));
        return;
      }
      into.push(chunk);
    };
    child.stdout.on('data', collect(stdout));
    child.stderr.on('data', collect(stderr));

    child.on('error', (err: NodeJS.ErrnoException) => {
      finish(() => {
        if (err.code === 'ENOENT' && !existsSync(request.cwd)) {
          // Node reports a missing working directory as ENOENT too.
          reject(new BinaryNotRunnableError(request.binary, `the folder ${request.cwd} does not exist`));
        } else if (err.code === 'ENOENT') {
          reject(new BinaryNotFoundError(request.binary));
        } else {
          reject(new BinaryNotRunnableError(request.binary, err.code !== undefined ? `${err.code}: ${err.message}` : err.message));
        }
      });
    });

    child.on('close', (code) => {
      finish(() =>
        resolve({
          code,
          stdout: Buffer.concat(stdout).toString('utf8'),
          stderr: Buffer.concat(stderr).toString('utf8'),
        }),
      );
    });
  });
};
