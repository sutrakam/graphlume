## Types, constants and module-level variables.
import std/[strutils, tables]

const
  Version = "1.2.0"
  MaxItems* = 256
  Names = ["a", "b"]

const Single = 5

let
  startedAt = 0
  banner* = "ready"

var
  counter = 0
  registry*: Table[string, int]

var first, second = 1

type
  Point* = object
    x*, y*: int

  Shape = ref object of RootObj
    name: string

  Kind* {.pure.} = enum
    kA, kB, kC

  Pair = tuple[a: int, b: string]

  Meters = distinct float

  Node*[T] = ref object
    value: T
    next: Node[T]

  Token = object
    case kind: bool
    of true: text: string
    else: number: int

  Handler = proc (x: int): int {.closure.}

  IntList = seq[int]

  Raw = ptr int

  Link = ref Point

  Both = int | string

  Showable = concept s
    $s is string

type Lone = object
  v: int

type Mode = enum
  mRead, mWrite

when defined(windows):
  const Sep = '\\'
  type WinHandle = distinct int
else:
  const Sep = '/'
  var posixOnly = 0

proc localTypes() =
  type Hidden = object
    z: int
  const Inner = 3
  let tmp = 1
  var scratch = 2
  discard (tmp, scratch, Inner)

echo Version, MaxItems, counter
