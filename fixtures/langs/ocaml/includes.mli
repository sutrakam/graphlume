module type S = sig
  type t

  include sig
    val length : t -> int
    val is_empty : t -> bool
  end

  include module type of struct
    include Base_s
  end

  include Container with type t := t

  type nonrec u := t

  module Inner : sig
    val inner_size : int
  end
  with type t = int
end

val last : int
