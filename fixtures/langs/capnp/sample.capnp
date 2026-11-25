@0xdbb9ad1f14bf0b36;

using Json = import "/capnp/compat/json.capnp";
using Cxx = import "/capnp/c++.capnp";

$Cxx.namespace("example");

const defaultPort :UInt16 = 8080;
const greeting :Text = "hello";

annotation priority @0xa1b2c3d4e5f60718 (field, struct) :UInt8;

enum Color {
  red @0;
  green @1;
  blue @2;
}

struct Point {
  x @0 :Float64;
  y @1 :Float64;
}

struct Shape {
  name @0 :Text;
  origin @1 :Point;
  color @2 :Color = red;

  union {
    circle @3 :Float64;
    rectangle :group {
      width @4 :Float64;
      height @5 :Float64;
    }
  }

  struct Style {
    filled @6 :Bool;
    union {
      solid @7 :Void;
      dashed @8 :UInt8;
    }
  }

  enum Kind {
    open @0;
    closed @1;
  }

  const unitScale :Float64 = 1.0;
}

interface Canvas {
  draw @0 (shape :Shape) -> (id :UInt32);
  clear @1 ();
  bounds @2 () -> (min :Point, max :Point);

  interface Layer {
    toggle @0 () -> (visible :Bool);
  }
}

interface Renderer extends(Canvas) {
  render @0 (scene :List(Shape)) -> (bytes :Data);
}

struct Pair(First, Second) {
  first @0 :First;
  second @1 :Second;
}

struct Request {
  using Name = Text;
  payload @0 :Pair(Name, Point);
  contact :union {
    email @1 :Text;
    phone @2 :Text;
  }
}
