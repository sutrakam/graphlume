module Shop
  module Helpers
    include Comparable
    extend Forwardable
    LIMIT = 10
  end

  class Request < Base
    include Helpers
    attr_reader :env, :path
    attr_writer :name
    attr_accessor :a, :b
    attr_reader dynamic_name
    alias new_name old_name
    alias :sym_new :sym_old
    alias_method :other_name, :orig_name
    define_method(:dyn) { |x| x }
    define_method("dyn2") { 1 }
    define_method(computed) { 1 }

    def initialize(env)
      super
      @env = env
      local = 1
    end

    def run(a)
      super(a, 1)
    end

    def x=(v)
      @x = v
    end

    def [](k)
      env[k]
    end

    def ==(other)
      true
    end

    def GET
      1
    end

    def self.build(a)
      new(a)
    end

    def self.create=(a); end

    def <=>(other); 0; end
    def +(other) = other
    def ready?; true; end
    def save!; end
    alias flag= flag_set=
  end
end
