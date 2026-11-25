export interface Options { retries: number }
export type Handler = (e: Event) => void;
export enum Mode { Fast, Slow }

export const API_URL: string = 'https://example.test';
export let current: number = 0;
export const defaults = { retries: 3 } as const;
export const schema = z.object({ id: z.string() });
export const handler = async (e: Event): Promise<void> => {};
export const Panel = memo(function Panel() { return 1; });
const secret = 'x';
const built = build();
const Ctx = createContext<number>(0);

export function load(opts: Options): number { return opts.retries; }

export class Client {
  private url: string;
  get(path: string): string { return this.url + path; }
}

export abstract class Base {
  abstract run(): void;
}

exports.legacy = function legacy() {};
Client.prototype.close = function close() {};

export class Cache {
  #store: Map<string, number> = new Map();
  static #hits = 0;
  #touch(key: string): void { this.#store.get(key); }
  get #size(): number { return this.#store.size; }
  public run(): void { this.#touch("a"); }
}

export function parse(input: string): string;
export function parse(input: number): number;
export function parse(input: any): any { return input; }

export namespace Util {
  export const LIMIT = 5;
  export function clamp(n: number): number { return n; }
}

interface Reader {
  read(n: number): string;
  read(n: string): string;
}

class Account {
  get balance(): number { return 1; }
  set balance(v: number) {}
}
