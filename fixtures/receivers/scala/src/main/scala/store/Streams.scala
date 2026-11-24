package store

// The unqualified supertype below is the file's own nested class. A supertype
// written `extends java.io.OutputStream` is the JDK's, and must not be bound to
// it (os-lib's SubProcess.scala): the parser records the qualifier as written,
// and internal/resolve/infer/supertype.go reads it.
object Streams { // def sc.Streams
  class OutputStream { // def sc.Streams.OutputStream
  }

  class Logging extends OutputStream { // def sc.Streams.Logging; super OutputStream -> sc.Streams.OutputStream
  }

  class Pumping extends java.io.OutputStream { // super OutputStream -> none
  }

  // By its owner, the nested class is named as a qualified supertype too.
  class Nested extends Streams.OutputStream { // super OutputStream -> sc.Streams.OutputStream
  }

  class Aliased(val out: java.io.OutputStream) extends java.io.OutputStream with Logging { // super OutputStream -> none; super Logging -> sc.Streams.Logging
  }
}
