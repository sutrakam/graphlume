package store

// A test helper that reuses a production name. Library code that calls
// `launcher.proc(...)` must not be bound to it by that name alone.
object TestUtil { // def sc.TestUtil
  def proc(cmd: String): String = cmd // def sc.TestUtil.proc
}
