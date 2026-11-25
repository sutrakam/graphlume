const std = @import("std");
const Build = std.Build;

pub const Handle = opaque {};
pub const Callback = fn (ctx: *anyopaque, code: i32) callconv(.c) void;
pub const Version = struct {
    major: u32,
    minor: u32,

    pub const current: Version = .{ .major = 0, .minor = 14 };

    pub const Parsed = struct {
        raw: []const u8,
    };

    pub fn format(self: Version, writer: anytype) !void {
        try writer.print("{d}.{d}", .{ self.major, self.minor });
    }
};

pub extern "c" fn puts(s: [*:0]const u8) c_int;
pub export fn zig_entry() callconv(.c) void {}

pub fn build(b: *Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const exe = b.addExecutable(.{
        .name = "app",
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
    });
    b.installArtifact(exe);

    var steps: [2]*Build.Step = undefined;
    steps[0] = &exe.step;
}

fn addAll(b: *Build, comptime names: []const []const u8) void {
    inline for (names) |name| {
        const local_fn = struct {
            fn run() void {}
        };
        _ = local_fn;
        _ = b;
        _ = name;
    }
}

const @"odd name" = 7;
var global_flag = false;
pub var buffer: [256]u8 = undefined;

const Limits = struct {
    pub var override: ?usize = null;
    const hard = 1024;
};

test "version format" {
    const v = Version{ .major = 1, .minor = 2 };
    _ = v;
}
