#pragma once

#define STORE_EXPORT __attribute__((visibility("default")))

namespace store {

class Cache;
struct Options;

class STORE_EXPORT Comparator {
 public:
  virtual ~Comparator();
  virtual int Compare(int a, int b) const = 0;
};

class STORE_EXPORT Env : public Comparator {
 public:
  Env();
  int Size() const { return size_; }
  static Env* Default();

 private:
  int size_;
};

struct STORE_EXPORT Options {
  int block_size;
};

class STORE_EXPORT Iterator final {
 public:
  void Next();
};

enum class STORE_EXPORT Mode { Read, Write };

template <typename T>
class Box {
 public:
  T Get() const { return value_; }

 private:
  T value_;
};

typedef Iterator* (*IteratorFactory)(void*, const Options&);
using Callback = int (*)(int);

STORE_EXPORT int Destroy(const char* name);

}  // namespace store
