import gleam/io
import gleam/list.{map}

pub const max_items = 10
const secret = "hidden"

pub type Shape {
  Circle(radius: Float)
  Square(side: Float)
}

type Color {
  Red
  Green
}

pub opaque type Stack(a) {
  Stack(items: List(a))
}

pub type Name =
  String

type Pair(a, b) =
  #(a, b)

pub fn area(shape: Shape) -> Float {
  case shape {
    Circle(r) -> 3.14 *. r *. r
    Square(s) -> s *. s
  }
}

fn helper(x: Int) -> Int {
  let doubled = x * 2
  let local = fn(y) { y + doubled }
  local(x)
}

pub fn describe(
  name: String,
  count: Int,
) -> String {
  io.println(name)
  name
}

@external(erlang, "lists", "reverse")
pub fn reverse(items: List(a)) -> List(a)

@external(javascript, "./ffi.mjs", "now")
fn now() -> Int

pub fn main() {
  let result = list.map([1, 2], helper)
  describe("a", 1)
  result
}

pub fn map_pair(pair: #(a, b), with fun: fn(a) -> c) -> #(c, b) {
  let #(first, second) = pair
  #(fun(first), second)
}

pub const names = ["a", "b"]

@external(erlang, "erlang", "self")
pub type Pid

pub fn with_use() {
  use value <- result.try(Ok(1))
  Ok(value)
}
