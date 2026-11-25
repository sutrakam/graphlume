library shapes;

import 'dart:math' as math;

const double pi2 = math.pi * 2;
final DateTime startedAt = DateTime.now();
var counter = 0;
int total = 5, limit = 10;
late String lateName;
const _hidden = 3;

typedef Callback = void Function(int value);
typedef Comparator<T> = int Function(T a, T b);
typedef void LegacyCallback(String message);

int add(int a, int b) => a + b;

Future<void> fetchAll(List<String> urls) async {
  final inner = (int x) => x + 1;
  print(inner(2));
}

String? get version => '1.0';
set version(String? value) {}

abstract class Shape {
  final String name;
  static const int sides = 0;
  static int created = 0;
  double _area = 0;
  late final String label;

  Shape(this.name);
  Shape.named(String n) : name = n;
  const Shape.constant(this.name);
  factory Shape.circle(double r) = Circle;
  factory Shape.empty() {
    return Circle(0);
  }

  double get area;
  set area(double value) {
    _area = value;
  }

  double perimeter();
  void describe() => print(name);

  static Shape unit() => Circle(1);
  static void reset() {}

  bool operator ==(Object other) => other is Shape;
  Shape operator +(Shape other) => this;
  int operator [](int i) => i;
  void operator []=(int i, int v) {}
  Shape operator -() => this;
  Shape operator ~() => this;
}

class Circle extends Shape with Printable implements Comparable<Circle> {
  final double radius;

  Circle(this.radius) : super('circle');

  @override
  double get area => 3.14 * radius * radius;

  @override
  double perimeter() => 2 * 3.14 * radius;

  @override
  int compareTo(Circle other) => 0;

  Future<List<int>> load<T>(T seed) async {
    return [];
  }
}

sealed class Result {}

final class Ok extends Result {
  Ok(this.value);
  final int value;
}

base class Foundation {}

interface class Contract {}

abstract interface class Service {
  void start();
}

mixin Printable {
  void printSelf() {}
  String get text => '';
}

mixin class Both {
  void both() {}
}

base mixin Tracked on Shape {
  int ticks = 0;
}

enum Color { red, green, blue }

enum Planet {
  mercury(3.3),
  venus(4.8);

  const Planet(this.mass);
  final double mass;

  double get weight => mass * 9.8;
  String describe() => 'planet';
  static Planet first() => mercury;
}

extension StringTools on String {
  String shout() => toUpperCase();
  int get doubled => length * 2;
  static String blank() => '';
}

extension on int {
  bool get isBig => this > 100;
}

extension type Meters(double value) {
  Meters.zero() : value = 0;
  Meters operator +(Meters other) => Meters(value + other.value);
  double get inKm => value / 1000;
}

void main() {
  var shape = Circle(2);
  shape.describe();
  void helper() {}
  helper();
}
