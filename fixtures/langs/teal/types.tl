-- Records, enums, interfaces and type aliases.
local json <const> = require("json")
local insert <const> = table.insert
local util <const> = require("cyan.util")

local record Point
  x: number
  y: number
  new: function(x: number, y: number): Point
end

global record Config
  name: string
  retries: integer

  record Limits
    low: integer
    high: integer
  end

  enum Level
    "debug"
    "info"
  end

  interface Visitor
    visit: function(self, node: Point)
  end

  type Callback = function(string): boolean
  type Names = {string}

  limits: Limits
end

local enum Color
  "red"
  "green"
  "blue"
end

local interface Shape
  area: function(self): number
end

local record Circle is Shape
  radius: number
end

local record Box<T>
  value: T
end

local type Id = integer
local type Handler = function(Point): boolean
global type Registry = {string:Point}
local type Both = Id | string

local x: integer = 1
local label: string = "text"
global counter: integer
local alias <const> = util.tab.ivalues
local DEFAULT_NAME <const> = "origin"
local MAX_POINTS <const>: integer = 100
local DEFAULTS <const> = { name = "a", retries = 3 }
local STEP <const> = -1
local cache = {}

return Point
