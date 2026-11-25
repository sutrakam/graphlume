open Printf
include Shapes

let limit : int = 5

let twice : (int -> int) -> int -> int = fun f x -> f (f x)

let typed (x : int) : int = x + 1

let identity = fun x -> x

let classify = function
  | 0 -> "zero"
  | _ -> "many"

let parenthesised = (fun x -> x)

let handler = ref (fun () -> ())

let[@inline] fast x = x * 2

type 'a ext = ..

type poly = [ `A | `B of int ]

type 'a opt = 'a option

type private_t = private A | B

module rec Even : sig
  val check : int -> bool
end = struct
  let check n = n = 0 || Odd.check (n - 1)
end

and Odd : sig
  val check : int -> bool
end = struct
  let check n = n <> 0 && Even.check (n - 1)
end

module Applied = Make (struct
  let seed = 1
end)

module type ORD = sig
  type t
  val compare : t -> t -> int
  val default : t
  module Sub : sig
    val deeper : int
  end
end

module Outer = struct
  module Middle = struct
    module Innermost = struct
      let at_depth_three = 3
    end
  end
end

let run () =
  let module Local = struct
    let hidden = 1
    type local_t = int
  end in
  let open Printf in
  let rec loop i = if i > 3 then () else loop (i + 1) in
  let f, g = (1, 2) in
  loop f + g + Local.hidden

class virtual shape_base name =
  object (self)
    val name : string = name
    val mutable moves = 0
    method virtual area : float
    method describe = sprintf "%s %f" name self#area
    initializer moves <- 0
  end

exception Failed of { code : int }

exception Alias = Not_found

external now : unit -> float = "caml_sys_time"
