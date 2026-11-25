// Move 2024 module label form: the module has no braces. The grammar cannot
// read it, so these definitions come from the ERROR patterns in lang_move.go.
module gallery::frames;

use std::string::String;
use sui::object::{Self, UID};

const EFrameFull: u64 = 10;
const MaxFrames: u64 = 64;

public struct Frame has key, store {
    id: UID,
    title: String,
}

public struct Slot() has copy, drop;

public struct Gallery<phantom T> has key {
    id: UID,
    frames: vector<Frame>,
}

public fun new_frame(title: String, ctx: &mut TxContext): Frame {
    Frame { id: object::new(ctx), title }
}

fun frame_count<T>(g: &Gallery<T>): u64 {
    g.frames.length()
}

public(package) fun check_room(g: &Gallery<u64>) {
    assert!(frame_count(g) < MaxFrames, EFrameFull);
}

entry fun publish(ctx: &mut TxContext) {
    let g = Gallery<u64> { id: object::new(ctx), frames: vector[] };
    transfer::share_object(g);
}
