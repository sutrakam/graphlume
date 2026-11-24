package demo;

enum Color {
  Red;
  Green;
  Blue;
}

typedef Alias = Array<Int>;

typedef Point = {
  var x:Int;
  var y:Int;
}

typedef Callback = String->Void;
