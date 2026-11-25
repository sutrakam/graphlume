#include "store.h"

#define STORE_INLINE inline
#define STORE_NO_SANITIZE(kind) __attribute__((no_sanitize(kind)))

namespace store {

Comparator::~Comparator() {}

Env::Env() : size_(0) {}

Env* Env::Default() {
  static Env env;
  return &env;
}

void Iterator::Next() {}

char* Name(int id) { return nullptr; }

const Options& Defaults() {
  static Options options;
  return options;
}

STORE_INLINE int Twice(int v) { return v * 2; }

static STORE_NO_SANITIZE("bounds") int At(const int* items, int index) {
  return items[index];
}

int store::Env::Peek() const { return Twice(size_); }

int Destroy(const char* name) {
  Env* env = Env::Default();
  return env->Size() + At(nullptr, 0);
}

}  // namespace store
