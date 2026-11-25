package demo;

import haxe.ds.Map;

interface Shape {
  var sides:Int;
  function area():Float;
  function name():String;
}

class Base {
  public function new() {
  }

  public function describe():String {
    return "base";
  }
}

class Circle extends Base implements Shape {
  public var sides:Int = 0;
  public static var count:Int = 0;
  private var radius:Float;
  static final PI:Float = 3.14;
  var label:String;

  public function new(r:Float) {
    super();
    radius = r;
    count++;
  }

  public function area():Float {
    var local:Float = radius * radius;
    return PI * local;
  }

  public function name():String {
    return "circle";
  }

  override public function describe():String {
    return "circle";
  }

  public static function create(r:Float):Circle {
    return new Circle(r);
  }

  private function helper(a:Int, b:Int):Int {
    var total = a + b;
    return total;
  }

  inline function scale(f:Float):Void {
    radius = radius * f;
  }
}

private class Hidden {
  var secret:Int;
}

final class Sealed {
  public static function run():Void {
    trace("run");
  }
}

function topLevel(a:Int):Int {
  return a;
}
