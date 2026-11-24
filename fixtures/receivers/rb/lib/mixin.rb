# `include Mod` in a class body declares a mixin; it is not a call. rack had it
# bound 18 times to the one `include` method the repository declares. The
# statement is recorded as a supertype reference and as no call at all.
class Recursive # def rb.Recursive
  def include(app) # def rb.Recursive.include
    app
  end
end

class Mixed # def rb.Mixed
  include Enumerable # nocall include
  extend Comparable # nocall extend
end
