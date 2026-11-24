// Classes, structs, enums, typedefs, namespaces and templates.
struct Point {
  int x;
  int y;
};

struct Reading {
  float value;
  const char *label;
  int history[4];
  Reading *next;
};

typedef unsigned char byte2;
typedef struct {
  int id;
} Pair;
typedef void (*Handler)(int);
using Callback = void (*)(int);
using Counter = unsigned long;

enum Mode { OFF, ON };
enum class State : unsigned char { Idle, Busy };

union Packet {
  int whole;
  float part;
};

class Sensor {
 public:
  Sensor(int pin);
  ~Sensor();
  int read();
  void begin() { started = true; }
  bool operator==(const Sensor &other) const { return pin == other.pin; }
  static int count;

 private:
  int pin;
  bool started;
  float scale = 1.0;
};

class Led;

Sensor::Sensor(int p) : pin(p), started(false) {}

Sensor::~Sensor() {}

int Sensor::read() {
  return analogRead(pin);
}

namespace util {
const int limit = 10;
int helper(int v) {
  return v;
}
}

template <typename T>
T maxOf(T a, T b) {
  return a > b ? a : b;
}

template <typename T>
class Buffer {
 public:
  void push(T v);
  T items[8];
};
