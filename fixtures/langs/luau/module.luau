--!strict
-- Functions in every form; return types are named types and must not leak.
export type Item = { name: string, qty: number }

local Inventory = {}
Inventory.__index = Inventory

local MAX_ITEMS = 100
local cache: { [string]: Item } = {}
local Registry: { Item } = {}

local function clamp(value: number, lo: number, hi: number): number
  return math.max(lo, math.min(hi, value))
end

local format = function(item: Item): string
  return item.name .. " x" .. tostring(item.qty)
end

function Inventory.new(): Inventory
  return setmetatable({ items = {} }, Inventory)
end

function Inventory.find(inv: Inventory, name: string, strict: boolean?): Item
  local found = inv.items[name]
  assert(not strict or found, "missing")
  return found
end

function Inventory:add(item: Item): ()
  local function normalise(it: Item): Item
    return { name = it.name, qty = clamp(it.qty, 0, MAX_ITEMS) }
  end
  self.items[item.name] = normalise(item)
end

function Inventory:count(): number
  local n = 0
  for _ in self.items do
    n += 1
  end
  return n
end

function Inventory.util.describe(inv: Inventory): string
  return format(inv.items.first)
end

Inventory.merge = function(a: Inventory, b: Inventory): Inventory
  return a
end

function report(inv: Inventory)
  print(inv:count(), cache, Registry)
end

Config = {
  retries = 3,
  load = function(path: string): Item
    return { name = path, qty = 1 }
  end,
}

local handlers = {
  ["onAdd"] = function(item: Item) end,
  onRemove = function(item: Item) end,
}

task.spawn(function()
  report(Inventory.new())
end)

return {
  Inventory = Inventory,
  create = function(): Inventory
    return Inventory.new()
  end,
  handlers = handlers,
}
