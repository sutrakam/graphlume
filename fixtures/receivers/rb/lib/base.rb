module Shop
  class Base
    def self.new(*args) # def Base.new
      super
    end

    def to_s # def Base.to_s
      'base'
    end

    def respond_to?(name) # def Base.respond_to
      true
    end
  end
end
