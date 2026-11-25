// Functions of every form.
use fmt;
use os;

fn plain(a: int) int = a + 1;

export fn exported(a: int, b: int) int = {
	return a + b;
};

export fn named_result(p: *point) point = {
	return *p;
};

fn aliased(p: point) handle = {
	return &p;
};

fn tagged(a: int) (int | error) = {
	return a;
};

fn variadic(prefix: str, rest: int...) void = {
	fmt::println(prefix, len(rest))!;
};

fn nested_ret(a: int) (point | void) = {
	if (a > 0) {
		return point { x = a, y = a };
	};
};

fn short(a: int) bool = a > 0;

fn novalue() void = void;

fn no_body(a: int) int;

@symbol("c_helper") fn helper(a: int) int;

export @symbol("c_other") fn other(a: *opaque) void;

@init fn startup() void = {
	counter = 1;
};

@fini fn shutdown() void = {
	counter = 0;
};

@test fn first_check() void = {
	assert(plain(1) == 2);
};

@test fn second_check() void = {
	assert(exported(1, 2) == 3);
};

@noreturn fn die(msg: str) void = {
	fmt::fatal(msg);
};

@fini @symbol("x") fn two_attributes() void = void;

fn main() void = {
	const r = plain(1);
	let total = exported(r, 2);
	total += 3;
	fmt::println(total)!;
	os::exit(0);
};
