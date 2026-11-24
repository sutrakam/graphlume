// Types, constants and globals.
use fmt;
use strings;

def MAXLEN: size = 256;
export def VERSION: str = "1.0";

let counter: int = 0;
export let shared: size = 0;
const limit: int = 10;
export const name: str = "demo";

type id = int;
export type text = str;
type bytes = []u8;
type grid = [4]int;
type maybe = nullable *int;
type scoped = strings::foo;

export type point = struct {
	x: int,
	y: int,
};

type packed = struct @packed {
	tag: u8,
	len: u16,
};

export type number = union {
	i: i64,
	f: f64,
};

export type color = enum u8 {
	RED,
	GREEN = 5,
	BLUE,
};

type pair = (int, str);
type result = (int | void | error);
type callback = fn(p: point) bool;
export type handle = *opaque;
type opaque = struct {
	fd: int,
};

export type error = !(
	nomem |
	invalid);
export type nomem = !void;
type invalid = !void;

type wrapped = struct {
	inner: struct {
		a: int,
	},
	u: union {
		b: int,
		c: str,
	},
};

fn describe(p: point) void = {
	let tmp = p.x;
	const k = p.y;
	fmt::println("x", tmp, k)!;
};
