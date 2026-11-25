module Walkable # def rb.Walkable
  def walk # def rb.Walkable.walk
    :walk
  end
end

# Two classes are named Animal (here and in vendor/animal.rb). The hierarchy is
# kept by name, so a call through it finds both and binds neither: it stays
# ambiguous rather than becoming wrong. The superclass EDGE below still goes to
# the right one.
class Animal # def rb.Animal
  def breathe # def rb.Animal.breathe
    :air
  end
end

# A different class that happens to share the method names and is not a
# supertype of anything below.
class Fish
  def breathe # def rb.Fish.breathe
    :water
  end

  def walk # def rb.Fish.walk
    :no
  end
end

class Dog < Animal # def rb.Dog; super Animal -> rb.Animal
  include Walkable # super Walkable -> rb.Walkable

  def bark # def rb.Dog.bark
    self.breathe # call breathe -> none
    self.walk # call walk -> rb.Walkable.walk
  end
end

class Puppy < Dog # super Dog -> rb.Dog
  extend Comparable # super Comparable -> none
  prepend Zoo::Loud # super Loud -> none
end

class Bird # def rb.Bird
  def fly # def rb.Bird.fly
    :up
  end
end

class Fish
  def fly # def rb.Fish.fly
    :no
  end
end

class Hen < Bird # super Bird -> rb.Bird
  def flap # def rb.Hen.flap
    self.fly # call fly -> rb.Bird.fly
  end
end
