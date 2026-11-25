//! A file that is itself a struct: its fields are written at file level.
const std = @import("std");
const Store = @This();

allocator: std.mem.Allocator,
items: std.ArrayListUnmanaged(u32) = .empty,
mutex: std.Thread.Mutex = .{},
/// Number of resets so far.
resets: usize = 0,
callback: ?*const fn (u32) void = null,

pub const Error = error{ OutOfSpace, Closed };

const Entry = struct {
    key: u32,
    value: []const u8,
};

pub fn init(allocator: std.mem.Allocator) Store {
    return .{ .allocator = allocator };
}

pub fn add(self: *Store, item: u32) !void {
    const local: u32 = item;
    try self.items.append(self.allocator, local);
}

fn reset(self: *Store) void {
    self.items.clearRetainingCapacity();
    self.resets += 1;
}

test "add one" {
    var s = Store.init(std.testing.allocator);
    try s.add(1);
}
