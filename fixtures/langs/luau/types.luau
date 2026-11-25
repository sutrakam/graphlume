--!strict
-- Type aliases of every shape.
type Point = { x: number, y: number }
export type Shape<T> = { origin: Point, data: T }
type Callback = (number, string) -> boolean
export type Handler<T, U> = (T) -> U
type Names = { string }
type Lookup = { [string]: number }
type Maybe<T> = T | nil
export type Both = Point & { z: number }
type Pending = typeof(setmetatable({}, {}))
type Alias = Point

local DEFAULT: Point = { x = 0, y = 0 }
local count: number = 0

local function area<T>(shape: Shape<T>): number
  return shape.origin.x * shape.origin.y
end

return area
