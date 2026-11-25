// The pieces that keep the CodeLens cheap: a bounded cache, a queue that
// limits how many processes run at once, and a gate that waits for typing to
// pause. None of them knows about VS Code or about GraphLume.

/** A least-recently-used cache with a hard size limit. */
export class BoundedCache<V> {
  private readonly entries = new Map<string, V>();

  constructor(private readonly maxEntries: number) {
    if (!Number.isInteger(maxEntries) || maxEntries < 1) {
      throw new RangeError('maxEntries must be a positive integer');
    }
  }

  get size(): number {
    return this.entries.size;
  }

  has(key: string): boolean {
    return this.entries.has(key);
  }

  get(key: string): V | undefined {
    if (!this.entries.has(key)) {
      return undefined;
    }
    const value = this.entries.get(key) as V;
    // Re-insert so the entry becomes the most recently used.
    this.entries.delete(key);
    this.entries.set(key, value);
    return value;
  }

  set(key: string, value: V): void {
    this.entries.delete(key);
    this.entries.set(key, value);
    while (this.entries.size > this.maxEntries) {
      const oldest = this.entries.keys().next();
      if (oldest.done === true) {
        break;
      }
      this.entries.delete(oldest.value);
    }
  }

  delete(key: string): void {
    this.entries.delete(key);
  }

  clear(): void {
    this.entries.clear();
  }
}

export class QueueOverflowError extends Error {
  constructor() {
    super('dropped: too many lookups were waiting');
    this.name = 'QueueOverflowError';
  }
}

export class AbortedError extends Error {
  constructor() {
    super('cancelled');
    this.name = 'AbortedError';
  }
}

interface Waiting<T> {
  readonly task: (signal: AbortSignal) => Promise<T>;
  readonly signal: AbortSignal;
  readonly resolve: (value: T) => void;
  readonly reject: (reason: unknown) => void;
  onAbort?: () => void;
}

/**
 * Runs at most `concurrency` tasks at a time and keeps at most `maxWaiting`
 * more in line. When the line is full the OLDEST waiting task is dropped:
 * the newest request is the one for what is on screen now.
 *
 * A task whose signal aborts while it waits never starts.
 */
export class TaskQueue {
  private running = 0;
  private readonly waiting: Waiting<unknown>[] = [];

  constructor(
    private readonly concurrency: number,
    private readonly maxWaiting: number,
  ) {
    if (!Number.isInteger(concurrency) || concurrency < 1) {
      throw new RangeError('concurrency must be a positive integer');
    }
    if (!Number.isInteger(maxWaiting) || maxWaiting < 0) {
      throw new RangeError('maxWaiting must be a non-negative integer');
    }
  }

  get active(): number {
    return this.running;
  }

  get pending(): number {
    return this.waiting.length;
  }

  run<T>(task: (signal: AbortSignal) => Promise<T>, signal: AbortSignal): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      if (signal.aborted) {
        reject(new AbortedError());
        return;
      }
      const entry: Waiting<T> = { task, signal, resolve, reject };
      if (this.running < this.concurrency) {
        this.start(entry);
        return;
      }
      entry.onAbort = () => {
        const index = this.waiting.indexOf(entry as Waiting<unknown>);
        if (index >= 0) {
          this.waiting.splice(index, 1);
          reject(new AbortedError());
        }
      };
      signal.addEventListener('abort', entry.onAbort, { once: true });
      this.waiting.push(entry as Waiting<unknown>);
      while (this.waiting.length > this.maxWaiting) {
        const dropped = this.waiting.shift();
        if (dropped === undefined) {
          break;
        }
        this.detach(dropped);
        dropped.reject(new QueueOverflowError());
      }
    });
  }

  private detach(entry: Waiting<unknown>): void {
    if (entry.onAbort !== undefined) {
      entry.signal.removeEventListener('abort', entry.onAbort);
    }
  }

  private start<T>(entry: Waiting<T>): void {
    this.running++;
    let work: Promise<T>;
    try {
      work = entry.task(entry.signal);
    } catch (err) {
      work = Promise.reject(err);
    }
    work.then(entry.resolve, entry.reject).finally(() => {
      this.running--;
      this.drain();
    });
  }

  private drain(): void {
    while (this.running < this.concurrency) {
      const next = this.waiting.shift();
      if (next === undefined) {
        return;
      }
      this.detach(next);
      if (next.signal.aborted) {
        next.reject(new AbortedError());
        continue;
      }
      this.start(next);
    }
  }
}

/**
 * Shares one in-flight promise between callers asking for the same key, so
 * two lenses for the same symbol start one process, not two.
 *
 * The shared work runs under its own signal and is aborted only when every
 * caller that joined it has gone away.
 */
export class InFlight<V> {
  private readonly flights = new Map<string, { promise: Promise<V>; controller: AbortController; waiters: number }>();

  get size(): number {
    return this.flights.size;
  }

  join(key: string, signal: AbortSignal, start: (signal: AbortSignal) => Promise<V>): Promise<V> {
    if (signal.aborted) {
      return Promise.reject(new AbortedError());
    }
    let flight = this.flights.get(key);
    if (flight === undefined) {
      const controller = new AbortController();
      const created = {
        controller,
        waiters: 0,
        promise: start(controller.signal).finally(() => {
          if (this.flights.get(key) === created) {
            this.flights.delete(key);
          }
        }),
      };
      // The shared promise may be rejected after every waiter has left.
      created.promise.catch(() => undefined);
      this.flights.set(key, created);
      flight = created;
    }
    const joined = flight;
    joined.waiters++;
    return new Promise<V>((resolve, reject) => {
      let settled = false;
      const onAbort = (): void => {
        if (settled) return;
        settled = true;
        joined.waiters--;
        if (joined.waiters <= 0) {
          joined.controller.abort();
        }
        reject(new AbortedError());
      };
      signal.addEventListener('abort', onAbort, { once: true });
      joined.promise.then(
        (value) => {
          if (settled) return;
          settled = true;
          signal.removeEventListener('abort', onAbort);
          resolve(value);
        },
        (err: unknown) => {
          if (settled) return;
          settled = true;
          signal.removeEventListener('abort', onAbort);
          reject(err);
        },
      );
    });
  }
}

export interface Clock {
  now(): number;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

export const systemClock: Clock = {
  now: () => Date.now(),
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (handle) => clearTimeout(handle as NodeJS.Timeout),
};

/**
 * Tracks when each document was last edited and lets a caller wait until it
 * has been left alone for `quietMs`. This is the debounce: a lookup for a
 * name that is still being typed never starts.
 */
export class QuietGate {
  private readonly lastTouched = new Map<string, number>();

  constructor(
    private readonly quietMs: number,
    private readonly clock: Clock = systemClock,
  ) {}

  /** Records an edit to the document with this key. */
  touch(key: string): void {
    this.lastTouched.set(key, this.clock.now());
  }

  forget(key: string): void {
    this.lastTouched.delete(key);
  }

  /** Resolves once the document has been quiet for `quietMs`; rejects if the signal aborts first. */
  whenQuiet(key: string, signal: AbortSignal): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      if (signal.aborted) {
        reject(new AbortedError());
        return;
      }
      let handle: unknown;
      const onAbort = (): void => {
        this.clock.clearTimeout(handle);
        reject(new AbortedError());
      };
      const check = (): void => {
        const touched = this.lastTouched.get(key);
        const remaining = touched === undefined ? 0 : touched + this.quietMs - this.clock.now();
        if (remaining <= 0) {
          signal.removeEventListener('abort', onAbort);
          resolve();
          return;
        }
        handle = this.clock.setTimeout(check, remaining);
      };
      signal.addEventListener('abort', onAbort, { once: true });
      check();
    });
  }
}
