open Belt

let maxItems = 10

let greeting = "hello"

let add = (a, b) => a + b

let rec fact = n =>
  if n <= 1 {
    1
  } else {
    n * fact(n - 1)
  }

let area = (shape: shape): float => {
  let half = 0.5
  switch shape {
  | Circle(r) => 3.14 *. r *. r
  | Square(s) => s *. s *. half
  }
}

type color = Red | Green | Blue

type shape =
  | Circle(float)
  | Square(float)

type point = {x: float, y: float}

type id = int

type pair<'a, 'b> = ('a, 'b)

type rec tree = Leaf | Node(tree, int, tree)

@module("node:fs")
external readFileSync: (string, string) => string = "readFileSync"

external now: unit => float = "Date.now"

module Stack = {
  type t = list<int>

  let empty = list{}

  let push = (stack, item) => list{item, ...stack}

  module Inner = {
    let depth = 2
  }
}

module type Printer = {
  type t
  let print: t => unit
}

module Console: Printer = {
  type t = string
  let print = s => Js.log(s)
}

module Short = Stack

exception NotFound(string)

let main = () => {
  let local = add(1, 2)
  let inner = x => x + local
  Js.log(inner(3))
  Stack.push(Stack.empty, 1)->ignore
}

let (a, b) = (1, 2)

let default = ()
