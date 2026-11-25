package store

// An object is called like a function: `store.isFile(p)` is the object's
// `apply`. os-lib's calls to os.isFile and os.exists were bound to the
// instance methods StatInfo.isFile and Path.exists, or left unresolved.
object isFile { // def sc.isFile
  def apply(p: String): Boolean = p.nonEmpty // def sc.isFile.apply
}

class StatInfo(val kind: String) { // def sc.StatInfo
  def isFile: Boolean = kind == "file" // def sc.StatInfo.isFile

  def isMissing: Boolean = kind.isEmpty // def sc.StatInfo.isMissing
}

object Listing { // def sc.Listing
  def check(p: String): Boolean = {
    store.isFile(p) // call isFile -> sc.isFile.apply high
  }

  def missing(p: String): Boolean = {
    store.isMissing(p) // call isMissing -> none
  }
}
