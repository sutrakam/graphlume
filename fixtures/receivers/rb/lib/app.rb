require_relative 'base'

module Shop
  class App
    def run(obj)
      @lock = Mutex.new # call new -> external
      name = String.new # call new -> external
      obj.respond_to?(:call) # call respond_to? -> none
      obj.to_s # call to_s -> none
      Base.new # call new -> Base.new high
    end
  end
end
