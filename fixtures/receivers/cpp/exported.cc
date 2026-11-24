// A class declared behind an export macro. The grammar does not know the macro
// and reads `class STORE_EXPORT Reader` as a class named STORE_EXPORT, so
// Reader was no symbol and a subclass bound to whatever else was called Reader.
#define STORE_EXPORT __attribute__((visibility("default")))

class Reader;

class STORE_EXPORT Reader { // def cpp.Reader
 public:
  int Fetch() { return 1; } // def cpp.Reader.Fetch
};

// Shares a method name with Reader and is not its base.
class Spooler {
 public:
  int Fetch() { return 2; } // def cpp.Spooler.Fetch
};

class STORE_EXPORT BufferedReader : public Reader { // def cpp.BufferedReader; super Reader -> cpp.Reader
 public:
  int Slurp() { // def cpp.BufferedReader.Slurp
    return this->Fetch(); // call Fetch -> cpp.Reader.Fetch
  }
};
