-- A class object and a returned table of functions.
local Animal = {}
Animal.__index = Animal

function Animal:new(name)
  local o = setmetatable({}, self)
  o.name = name
  return o
end

function Animal:speak()
  return self.name .. " makes a sound"
end

local Dog = setmetatable({}, { __index = Animal })

function Dog:speak()
  local function bark()
    return "woof"
  end
  return bark()
end

function Dog.create(name)
  return Dog:new(name)
end

local function counter()
  local n = 0
  return function()
    n = n + 1
    return n
  end
end

local next_id = counter()
local pets = {}
pets[1] = Dog.create("rex")
pets[2] = Animal:new("generic")
for _, p in ipairs(pets) do
  print(p:speak(), next_id())
end

return {
  Animal = Animal,
  make = function(kind, name)
    local pick = function(k) return k == "dog" end
    if pick(kind) then return Dog.create(name) end
    return Animal:new(name)
  end,
  ["alias"] = function(name) return Animal:new(name) end,
  version = "1.0",
}
