// A sketch: macros, globals, setup/loop and helper functions.
#include <Servo.h>

#define LED_PIN 13
#define BAUD 9600
#define SQUARE(x) ((x) * (x))
#define CLAMP(v, lo, hi) ((v) < (lo) ? (lo) : ((v) > (hi) ? (hi) : (v)))

const int buttonPin = 2;
constexpr int sampleCount = 16;
const byte pins[4] = {3, 4, 5, 6};
const char *greeting = "hello";

int ledState = HIGH;
int a = 1, b = 2;
unsigned long lastChange = 0;
static unsigned long lastReport;
volatile bool flag = false;
float readings[8];
int grid[4][4];
char *buffer;
Servo myServo;

#ifdef DEBUG
int debugLevel = 2;
#else
int debugLevel = 0;
#endif

int clampPin(int pin);
void report(const char *msg, int value);

void setup() {
  int local = 3;
  pinMode(LED_PIN, OUTPUT);
  Serial.begin(BAUD);
  myServo.attach(9);
  report(greeting, local);
}

void loop() {
  int reading = digitalRead(buttonPin);
  if (reading != ledState) {
    ledState = reading;
    digitalWrite(LED_PIN, ledState);
  }
  report("tick", clampPin(reading));
}

int clampPin(int pin) {
  int result = CLAMP(pin, 0, 13);
  return result;
}

void report(const char *msg, int value) {
  Serial.print(msg);
  Serial.println(value);
}

static inline long twice(long v) {
  return v * 2;
}

char *nameOf(int id) {
  return buffer;
}
