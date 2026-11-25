package sample

import "core:fmt"
import "core:strings"

MAX_ITEMS :: 64
Greeting :: "hello"
ratio :: 1.5

counter: int
limit: int = 10

Vec2 :: struct {
	x, y: f32,
	label: string,
}

Color :: enum {
	Red,
	Green,
	Blue,
}

Shape :: union {
	Circle,
	Square,
}

Circle :: struct {
	center: Vec2,
	radius: f32,
}

Square :: struct {
	origin: Vec2,
	side:   f32,
}

Id :: distinct int

Flags :: bit_set[Color]

Pair :: struct($A, $B: typeid) {
	first:  A,
	second: B,
}

add :: proc(a, b: int) -> int {
	total := a + b
	return total
}

area :: proc(s: Shape) -> f32 {
	switch v in s {
	case Circle:
		return 3.14 * v.radius * v.radius
	case Square:
		return v.side * v.side
	}
	return 0
}

swap :: proc(pair: ^Pair($A, $B)) {
	fmt.println(pair.first)
}

length :: proc(v: Vec2) -> f32 {
	helper :: proc(n: f32) -> f32 {
		return n * n
	}
	return helper(v.x) + helper(v.y)
}

@(private)
secret :: proc() {
	fmt.println(Greeting)
}

main :: proc() {
	v := Vec2{1, 2}
	n := add(1, 2)
	fmt.println(n, length(v))
}

add_f32 :: proc(a, b: f32) -> f32 {
	return a + b
}

add_any :: proc{add, add_f32}

foreign import libc "system:c"

foreign libc {
	puts :: proc(s: cstring) -> i32 ---
	abs  :: proc(n: i32) -> i32 ---
}

when ODIN_OS == .Windows {
	platform_name :: proc() -> string {
		return "windows"
	}
} else {
	platform_name :: proc() -> string {
		return "other"
	}
}

Tree :: struct {
	left, right: ^Tree,
	value:       int,
}

insert :: proc(t: ^Tree, v: int) -> ^Tree #no_bounds_check {
	if t == nil {
		return new_clone(Tree{value = v})
	}
	return t
}

Kind :: enum u8 {
	A = 1,
	B,
}

Option :: union #no_nil {
	int,
	string,
}

Mask :: bit_field u32 {
	low:  u8 | 4,
	high: u8 | 4,
}

Name :: distinct string

names := [?]string{"a", "b"}
threshold := 3
