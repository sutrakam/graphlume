const std = @import("std");
const mem = std.mem;
const Allocator = mem.Allocator;
const assert = std.debug.assert;

pub const max_items: usize = 64;
const default_name = "stack";
pub var counter: u32 = 0;
var scratch: [16]u8 = undefined;
threadlocal var depth: usize = 0;

pub const Error = error{ Overflow, Empty };
const ParseError = error{InvalidInput} || Error;

pub const Color = enum(u8) {
    red,
    green = 2,
    blue,

    pub fn isWarm(self: Color) bool {
        return self == .red;
    }

    const count = 3;
};

pub const Shape = union(enum) {
    circle: f32,
    square: f32,

    pub fn area(self: Shape) f32 {
        return switch (self) {
            .circle => |r| r * r * 3.14,
            .square => |s| s * s,
        };
    }
};

pub const Point = struct {
    x: i32,
    y: i32 = 0,

    pub const origin = Point{ .x = 0, .y = 0 };

    pub fn init(x: i32, y: i32) Point {
        return .{ .x = x, .y = y };
    }

    pub fn add(self: Point, other: Point) Point {
        return .{ .x = self.x + other.x, .y = self.y + other.y };
    }

    fn private(self: *Point) void {
        _ = self;
    }
};

const Pair = extern struct { a: u32, b: u32 };
const Packed = packed struct(u8) { flag: bool, rest: u7 };
const Handler = *const fn (i32) void;
const Alias = u64;
const Mapped = std.AutoHashMap(u32, []const u8);

pub fn Stack(comptime T: type) type {
    return struct {
        const Self = @This();

        items: []T,
        len: usize = 0,

        pub fn init(buffer: []T) Self {
            return .{ .items = buffer };
        }

        pub fn push(self: *Self, value: T) Error!void {
            if (self.len == self.items.len) return error.Overflow;
            self.items[self.len] = value;
            self.len += 1;
        }

        pub fn pop(self: *Self) ?T {
            if (self.len == 0) return null;
            self.len -= 1;
            return self.items[self.len];
        }
    };
}

pub fn main() !void {
    var buf: [4]i32 = undefined;
    var s = Stack(i32).init(&buf);
    try s.push(1);
    const local = s.pop();
    _ = local;
}

fn helper(a: i32, b: i32) i32 {
    const sum = a + b;
    return sum;
}

inline fn fast(x: u32) u32 {
    return x << 1;
}

pub inline fn shift(x: u32) u32 {
    return x >> 1;
}

export fn exported(x: c_int) c_int {
    return x;
}

extern fn c_call(x: c_int) c_int;

fn comptimeSize(comptime n: usize) usize {
    return n * 2;
}

fn Wrapper(comptime T: type) type {
    return struct { value: T };
}

comptime {
    assert(max_items > 0);
}

test "stack push and pop" {
    var buf: [2]i32 = undefined;
    var s = Stack(i32).init(&buf);
    try s.push(5);
    try std.testing.expectEqual(@as(?i32, 5), s.pop());
}

test helper {
    try std.testing.expect(helper(1, 2) == 3);
}
