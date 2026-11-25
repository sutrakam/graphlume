-- Functions and methods in every form.
local record Stack
  items: {any}
  new: function(): Stack
end

local record Parser
  record Token
    text: string
  end
  source: string
end

local function trim(s: string): string
  local function inner(x: string): string
    return x
  end
  return inner(s)
end

local function generic<T>(a: T): T
  return a
end

function Stack.new(): Stack
  return setmetatable({}, { __index = Stack })
end

function Stack:push(v: any)
  self.items[#self.items + 1] = v
end

function Stack:pop(): any
  return table.remove(self.items)
end

function Stack.util.describe(s: Stack): string
  return "stack of " .. #s.items
end

function Parser.Token:show(): string
  return self.text
end

function Parser:parse<T>(input: string): T
  return nil
end

global function log_all(msg: string)
  print(trim(msg))
end

local handler = function(a: integer): integer
  return a + 1
end

global export_fn = function(s: string): string
  return s
end

local callbacks = {
  on_start = function() end,
}

local pending: {string} = {}

local function use_all()
  local stack = Stack.new()
  stack:push(handler(1))
  local nested = function() return trim("x") end
  print(stack:pop(), nested(), generic(1))
end

use_all()
