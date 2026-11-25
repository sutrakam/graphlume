## Routines of every kind, exported and not.
import std/strutils

type
  Account* = ref object
    balance: int

proc plain(a: int): int = a + 1

proc exported*(a: int): int = a + 2

func pure*(a, b: int): int =
  a * b

proc generic*[T](x: T): T = x

proc withPragma*(x: int): int {.inline, noSideEffect.} =
  x

method area*(a: Account): int {.base.} =
  a.balance

method describe(a: Account): string =
  "account"

iterator items*(n: int): int =
  for i in 0 ..< n:
    yield i

iterator pairsOf(n: int): (int, int) =
  yield (n, n)

template twice*(body: untyped) =
  body
  body

template unexported(x: int): int = x

macro dump*(x: untyped): untyped =
  result = x

converter toStr*(x: int): string = $x

proc `+`*(a, b: Account): Account = a

proc `$`(a: Account): string = "acct"

proc `[]`*(a: Account, i: int): int = i

proc `==`*[T](a, b: T): bool = true

template `|`(a, b: string): string = a & b

proc forward(a: int): int

proc forward(a: int): int = a

proc setter*(a: Account, v: int) {.importc: "set_it", cdecl.}

proc outer(n: int): int =
  proc helper(x: int): int = x + n
  let local = helper(1)
  var other = 2
  const limit = 3
  result = local + other + limit

when defined(js):
  proc jsOnly*(): int = 1
else:
  proc nativeOnly*(): int = 2

proc useThem() =
  echo plain(1), exported(2), pure(1, 2), generic(3)
  for x in items(3):
    echo x
  twice:
    echo "hi"

useThem()
