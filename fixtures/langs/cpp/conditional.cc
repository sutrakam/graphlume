// Conditional compilation that cuts a declaration apart.

namespace store {

class Table {
 public:
  Table(int size, int flags)
      : size_(size),
#ifdef TABLE_COUNTS
        count_(0),
#endif
        flags_(flags) {}

  int size() const { return size_; }

 private:
  int size_;
#ifdef TABLE_COUNTS
  int count_;
#endif
  int flags_;
};

class Cache {
 public:
  int Get(int key,
#ifdef CACHE_STATS
          int weight)
#else
          int mode)
#endif
  {
    return key;
  }

  void Reset() { hits_ = 0; }

 private:
  int hits_;
};

#ifdef __cplusplus
extern "C" {
#endif

int open_store(int flags);

#ifdef __cplusplus
}
#endif

}  // namespace store
