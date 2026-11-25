package store

object Runner { // def sc.Runner
  def start(cmd: String): String = {
    os.proc(cmd) // call proc -> none
  }
}
