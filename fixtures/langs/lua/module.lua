-- A module table with plain, dotted and colon functions.
local Stack = {}
Stack.__index = Stack

local MAX_DEPTH = 64
local helper_count = 0

local function trim(s)
  local function inner(x)
    return x:gsub("^%s+", "")
  end
  return inner(s):gsub("%s+$", "")
end

local clamp = function(v, lo, hi)
  if v < lo then return lo end
  if v > hi then return hi end
  return v
end

function Stack.new(limit)
  local self = setmetatable({}, Stack)
  self.items = {}
  self.limit = limit or MAX_DEPTH
  return self
end

function Stack:push(v)
  self.items[#self.items + 1] = v
  helper_count = helper_count + 1
end

function Stack:pop()
  local v = self.items[#self.items]
  self.items[#self.items] = nil
  return v
end

function Stack.util.describe(s)
  return "stack of " .. #s.items
end

Stack.size = function(self)
  return #self.items
end

Stack.on_full = function(self, cb)
  self.cb = cb
end

function global_log(msg)
  print(trim(msg))
end

Registry = {}

Handler = function(evt)
  return clamp(evt, 0, 10)
end

Stack.defaults = {
  depth = 4,
  name = "stack",
  builder = function() return Stack.new(4) end,
  hooks = {
    before = function(self) end,
    label = "hooks",
  },
}

local cfg = { retries = 3, sink = print }
local n = 10
local s = "text"
print(trim("  x  "), cfg.retries, n, s)
table.sort(cfg, function(a, b) return a < b end)
setmetatable(cfg, { __index = function(t, k) return k end })

return Stack
