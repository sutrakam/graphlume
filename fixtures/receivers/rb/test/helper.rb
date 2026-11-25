# A test double. `request.env` in library code (lib/envreader.rb) is a
# Rack::Request's, and the double's `env` is not reachable from it.
class NothingMiddleware # def rb.NothingMiddleware
  def env # def rb.NothingMiddleware.env
    {}
  end
end
