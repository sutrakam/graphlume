import 'package:flutter/widgets.dart';

part 'widgets.g.dart';

const kPadding = EdgeInsets.all(8);
final List<int> primes = <int>[2, 3, 5];
Map<String, int>? registry;

typedef Json = Map<String, dynamic>;
typedef Mapper<S, T extends Object> = T Function(S source);
typedef int Compare(int a, int b);

external int nativeCall(int x);

Stream<int> countTo(int n) async* {
  for (var i = 0; i < n; i++) {
    yield i;
  }
}

T identity<T>(T value) => value;

class Point {
  final int x;
  final int y;
  const Point({required this.x, this.y = 0});
  const Point.origin() : this(x: 0);
  Point.fromJson(Json json)
      : x = json['x'] as int,
        y = json['y'] as int;

  factory Point.parse(String text) {
    final parts = text.split(',');
    return Point(x: int.parse(parts[0]));
  }

  @override
  bool operator ==(Object other) => other is Point && other.x == x;

  @override
  int get hashCode => Object.hash(x, y);

  Point._();
}

class MyApp extends StatelessWidget {
  const MyApp({super.key});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(color: theme.primaryColor);
  }
}

class Counter extends StatefulWidget {
  const Counter({Key? key, required this.start}) : super(key: key);
  final int start;

  @override
  State<Counter> createState() => _CounterState();
}

class _CounterState extends State<Counter> {
  late int _count;
  int? cached;
  static const Duration tick = Duration(seconds: 1);
  static final List<String> names = [];

  @override
  void initState() {
    super.initState();
    _count = widget.start;
  }

  void _increment() {
    setState(() {
      _count++;
    });
  }

  external void native();

  @override
  Widget build(BuildContext context) => Text('$_count');
}

class Both = Point with Printable;

abstract mixin class Logger {
  void log(String message);
}

mixin Printable<T> on Point implements Comparable<T> {
  String render() => '';
}

enum Status implements Comparable<Status> {
  active('A'),
  inactive('I');

  const Status(this.code);
  final String code;

  @override
  int compareTo(Status other) => code.compareTo(other.code);
}

extension IterableX<T> on Iterable<T> {
  T? get firstOrNull => isEmpty ? null : first;
  List<T> toListSafe() => toList();
}

extension type const Id(int value) implements int {
  bool get isValid => value > 0;
}
