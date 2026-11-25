import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { Clock } from '../src/core/cache';
import { AbortedError, BoundedCache, InFlight, QueueOverflowError, QuietGate, TaskQueue } from '../src/core/cache';

function deferred<T>(): { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void } {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const tick = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

test('BoundedCache evicts the least recently used entry', () => {
  const cache = new BoundedCache<number>(2);
  cache.set('a', 1);
  cache.set('b', 2);
  assert.equal(cache.get('a'), 1); // a is now the most recent
  cache.set('c', 3);
  assert.equal(cache.has('b'), false);
  assert.equal(cache.get('a'), 1);
  assert.equal(cache.get('c'), 3);
  assert.equal(cache.size, 2);
  cache.delete('a');
  cache.clear();
  assert.equal(cache.size, 0);
  assert.throws(() => new BoundedCache<number>(0), RangeError);
});

test('TaskQueue never runs more than its concurrency', async () => {
  const queue = new TaskQueue(2, 10);
  const never = new AbortController().signal;
  let running = 0;
  let peak = 0;
  const gates = Array.from({ length: 6 }, () => deferred<void>());
  const results = gates.map((gate, i) =>
    queue.run(async () => {
      running++;
      peak = Math.max(peak, running);
      await gate.promise;
      running--;
      return i;
    }, never),
  );
  await tick();
  assert.equal(queue.active, 2);
  assert.equal(queue.pending, 4);
  for (const gate of gates) {
    gate.resolve();
    await tick();
  }
  assert.deepEqual(await Promise.all(results), [0, 1, 2, 3, 4, 5]);
  assert.equal(peak, 2);
  assert.equal(queue.active, 0);
});

test('TaskQueue drops the oldest waiting task when the line is full', async () => {
  const queue = new TaskQueue(1, 2);
  const never = new AbortController().signal;
  const gate = deferred<void>();
  const started: string[] = [];
  const first = queue.run(async () => {
    started.push('first');
    await gate.promise;
  }, never);
  const a = queue.run(async () => void started.push('a'), never);
  const b = queue.run(async () => void started.push('b'), never);
  const c = queue.run(async () => void started.push('c'), never);
  await assert.rejects(a, QueueOverflowError);
  gate.resolve();
  await Promise.all([first, b, c]);
  assert.deepEqual(started, ['first', 'b', 'c']);
});

test('TaskQueue: a task cancelled while waiting never starts', async () => {
  const queue = new TaskQueue(1, 5);
  const never = new AbortController().signal;
  const gate = deferred<void>();
  let ran = false;
  const first = queue.run(() => gate.promise, never);
  const controller = new AbortController();
  const waiting = queue.run(async () => {
    ran = true;
  }, controller.signal);
  controller.abort();
  await assert.rejects(waiting, AbortedError);
  assert.equal(queue.pending, 0);
  gate.resolve();
  await first;
  await tick();
  assert.equal(ran, false);

  const already = new AbortController();
  already.abort();
  await assert.rejects(
    queue.run(async () => 1, already.signal),
    AbortedError,
  );
});

test('TaskQueue keeps going after a task fails', async () => {
  const queue = new TaskQueue(1, 5);
  const never = new AbortController().signal;
  await assert.rejects(
    queue.run(async () => {
      throw new Error('boom');
    }, never),
    /boom/u,
  );
  await assert.rejects(
    queue.run(() => {
      throw new Error('sync boom');
    }, never),
    /sync boom/u,
  );
  assert.equal(await queue.run(async () => 7, never), 7);
});

test('InFlight starts one piece of work for the same key', async () => {
  const flights = new InFlight<number>();
  const never = new AbortController().signal;
  const gate = deferred<number>();
  let starts = 0;
  const start = (): Promise<number> => {
    starts++;
    return gate.promise;
  };
  const one = flights.join('k', never, start);
  const two = flights.join('k', never, start);
  const other = flights.join('other', never, async () => 9);
  assert.equal(starts, 1);
  gate.resolve(5);
  assert.deepEqual(await Promise.all([one, two, other]), [5, 5, 9]);
  assert.equal(flights.size, 0);
  // Once it has landed, the same key starts fresh work.
  assert.equal(await flights.join('k', never, async () => 6), 6);
});

test('InFlight aborts the shared work only when every caller has left', async () => {
  const flights = new InFlight<number>();
  const gate = deferred<number>();
  let sharedSignal: AbortSignal | undefined;
  const start = (signal: AbortSignal): Promise<number> => {
    sharedSignal = signal;
    return gate.promise;
  };
  const a = new AbortController();
  const b = new AbortController();
  const first = flights.join('k', a.signal, start);
  const second = flights.join('k', b.signal, start);
  a.abort();
  await assert.rejects(first, AbortedError);
  assert.equal(sharedSignal?.aborted, false);
  b.abort();
  await assert.rejects(second, AbortedError);
  assert.equal(sharedSignal?.aborted, true);
  gate.reject(new Error('stopped'));
  await tick();
});

class FakeClock implements Clock {
  time = 0;
  private timers: { at: number; fn: () => void; id: number }[] = [];
  private nextId = 1;
  now(): number {
    return this.time;
  }
  setTimeout(fn: () => void, ms: number): unknown {
    const id = this.nextId++;
    this.timers.push({ at: this.time + ms, fn, id });
    return id;
  }
  clearTimeout(handle: unknown): void {
    this.timers = this.timers.filter((t) => t.id !== handle);
  }
  advance(ms: number): void {
    this.time += ms;
    const due = this.timers.filter((t) => t.at <= this.time);
    this.timers = this.timers.filter((t) => t.at > this.time);
    for (const t of due) t.fn();
  }
  get pendingTimers(): number {
    return this.timers.length;
  }
}

test('QuietGate waits until typing has paused, and each keystroke pushes the wait back', async () => {
  const clock = new FakeClock();
  const gate = new QuietGate(1000, clock);
  const never = new AbortController().signal;

  // A document nobody has edited is quiet already.
  await gate.whenQuiet('untouched', never);

  gate.touch('doc');
  let quiet = false;
  const waiting = gate.whenQuiet('doc', never).then(() => {
    quiet = true;
  });
  clock.advance(600);
  gate.touch('doc'); // another keystroke at t=600
  clock.advance(400); // t=1000: the first deadline, but the document was touched since
  await tick();
  assert.equal(quiet, false);
  clock.advance(599); // t=1599
  await tick();
  assert.equal(quiet, false);
  clock.advance(1); // t=1600: 1000 ms after the last keystroke
  await waiting;
  assert.equal(quiet, true);
});

test('QuietGate: a cancelled wait rejects and leaves no timer behind', async () => {
  const clock = new FakeClock();
  const gate = new QuietGate(1000, clock);
  gate.touch('doc');
  const controller = new AbortController();
  const waiting = gate.whenQuiet('doc', controller.signal);
  assert.equal(clock.pendingTimers, 1);
  controller.abort();
  await assert.rejects(waiting, AbortedError);
  assert.equal(clock.pendingTimers, 0);

  gate.forget('doc');
  await gate.whenQuiet('doc', new AbortController().signal);
});
