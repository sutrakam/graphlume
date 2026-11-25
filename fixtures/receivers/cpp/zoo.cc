class Mammal { // def cpp.Mammal
 public:
  void Breathe() {} // def cpp.Mammal.Breathe
};

// Shares a method name with Mammal and is not its base.
class Trout {
 public:
  void Breathe() {} // def cpp.Trout.Breathe
};

class Walker { // def cpp.Walker
 public:
  void Walk() {} // def cpp.Walker.Walk
};

class Wolf : public Mammal, private Walker { // def cpp.Wolf; super Mammal -> cpp.Mammal; super Walker -> cpp.Walker
 public:
  void Bark() { // def cpp.Wolf.Bark
    this->Breathe(); // call Breathe -> cpp.Mammal.Breathe
    Walk(); // call Walk -> cpp.Walker.Walk
  }
};

struct Cub : Wolf {}; // super Wolf -> cpp.Wolf

class Plant {}; // def cpp.Plant

class Tree : public Plant {}; // super Plant -> cpp.Plant

template <typename T>
class Box : public ns::Base<T> {}; // super Base -> none

class ostream {}; // def cpp.ostream

class Quiet : public ostream {}; // super ostream -> cpp.ostream

class Loud : public std::ostream {}; // super ostream -> none
