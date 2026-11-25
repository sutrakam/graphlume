package store

// A secondary constructor delegates to another constructor of its class, never to
// itself (os-lib CoreServicesApi.scala: `def this() = this(0)`). The primary
// constructor is not a symbol of its own, so a delegation to it binds to nothing.
class Pair(val a: Int, val b: Int) { // def sc.Pair
  def this(a: Int) = this(a, 0) // def sc.Pair.one; call this -> none

  def this() = this(1) // def sc.Pair.zero; call this -> sc.Pair.one
}

// Another class with constructors of the same name is not a candidate.
class Triple(val a: Int) { // def sc.Triple
  def this() = this(0) // def sc.Triple.zero; call this -> none
}
