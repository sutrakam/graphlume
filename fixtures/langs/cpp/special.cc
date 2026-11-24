// Defaulted and deleted special members are declarations without a body: they
// are not listed, and the constructor with a body is.
class Guard final {
 public:
  Guard() = default;
  explicit Guard(int fd) : fd_(fd) {}
  Guard(Guard&&) = delete;
  Guard(const Guard&) = delete;
  ~Guard() = default;
  void Run() = delete;

 private:
  int fd_ = 0;
};
