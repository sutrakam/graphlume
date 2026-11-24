package store

import store.util.Tally

// An explicit import outranks a class of the same name in the file's package.
object Counter { // def sc.Counter
  def make() = new Tally() // call Tally -> new sc.util.Tally
  def made() = Tally() // call Tally -> new sc.util.Tally
}
