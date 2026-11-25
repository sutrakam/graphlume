open struct
  module Either = Either0
  module Option = Option0
end

include struct
  let default_size = 16
  let make_name prefix = prefix ^ "_name"

  module type Sized = sig
    type t [@@deriving sexp_of]

    val size : t -> int
  end

  include struct
    let nested_default = 3
  end
end

module Parts : sig
  type outer
  type t

  val fractional : t -> outer
end
with type outer := float

module type Wrapped = sig
  val wrapped : int
end
with type t = int

module Creators (Key : sig
    type key
    val hash : key -> int
  end) : sig
  type 'a table

  val create : unit -> 'a table
end

module Deep = struct
  module L1 = struct
    module L2 = struct
      module L3 = struct
        module L4 = struct
          let bottom = 4
        end
      end
    end
  end
end

type 'a opaque [@@deriving sexp_of]

let outer_function x =
  let module Local = struct
    let hidden = x

    type local_t = int
  end
  in
  let y = Local.hidden in
  y

module Applied = Make (struct
    let seed = 1
  end)
