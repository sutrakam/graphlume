(** Interface of the shapes library. *)

val pi : float

val square : int -> int

val area_of_circle : float -> float
(** Documented. *)

val ( +++ ) : int -> int -> int

val labelled : count:int -> ?step:int -> unit -> int

type color = Red | Green | Blue of int

type point = { x : float; y : float }

type t

type id = int

type 'a tree =
  | Leaf
  | Node of 'a tree * 'a * 'a tree

exception Bad of string

external caml_hash : int -> int = "caml_hash_c"

module Geometry : sig
  val origin : point

  val distance : point -> point -> float

  type kind = Circle | Square
end

module type SHAPE = sig
  val area : float -> float

  type s
end

module Make (S : SHAPE) : sig
  val scale : float -> float
end

class counter : int -> object
  method incr : unit
  method get : int
end

class type printable = object
  method print : unit
end

val callbacks : (int -> unit) list
