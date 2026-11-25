(* Declarations at module level *)
let pi = 3.14159

let square x = x * x

let rec fact n = if n <= 1 then 1 else n * fact (n - 1)

let rec even n = n = 0 || odd (n - 1)
and odd n = n <> 0 && even (n - 1)

let (|>>) x f = f x

let ( +++ ) a b = a + b

let add_pair (a, b) = a + b

let area_of_circle r =
  let r2 = square r in
  let factor = pi in
  r2 *. factor

let () = print_endline "side effect"

let _ = ignore 1

let (a, b) = (1, 2)

let labelled ~count ?(step = 1) () =
  let total = ref 0 in
  for i = 0 to count do
    total := !total + (i * step)
  done;
  !total

type color = Red | Green | Blue of int

type point = { x : float; y : float }

type 'a tree =
  | Leaf
  | Node of 'a tree * 'a * 'a tree

type id = int

type t = A | B
and u = { f : t }

type nonrec shadow = int

exception Not_found_here of string

exception Bad

external caml_hash : int -> int = "caml_hash_c"

module Geometry = struct
  let origin = { x = 0.; y = 0. }

  let distance p q =
    let dx = p.x -. q.x in
    let dy = p.y -. q.y in
    sqrt ((dx *. dx) +. (dy *. dy))

  module Inner = struct
    let deep = 1
  end

  type kind = Circle | Square
end

module type SHAPE = sig
  val area : float -> float
  type t
end

module Make (S : SHAPE) = struct
  let scale k = k *. 2.
end

module Alias = Geometry

module F (X : SHAPE) : SHAPE = struct
  type t = unit
  let area _ = 0.
end

class counter init =
  object (self)
    val mutable count = init
    method incr = count <- count + 1
    method get = count
    method private reset = count <- 0
  end

class type printable = object
  method print : unit
end

class virtual animal = object
  method virtual name : string
end

let use_local () =
  let helper y = y + 1 in
  let module M = struct let inner = 2 end in
  helper M.inner

let m = match 1 with
  | 1 -> let tmp = 2 in tmp
  | _ -> 0
