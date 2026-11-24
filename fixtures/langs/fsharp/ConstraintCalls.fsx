module Acme.Awaiting

open System
open System.Threading.Tasks

// A call such as (^T: (member Name: unit -> int) x) in a comment is left alone.
let note = "(^T: (member GetAwaiter: unit -> ^A) (task))"

let inline awaiterOf (task: ^T) =
    let a = (^T: (member GetAwaiter: unit -> ^A) (task))
    a

let inline isDone (awaiter: ^A) =
    if (^A: (member get_IsCompleted: unit -> bool) awaiter) then true else false

let inline convert (x: ^a) : ^b =
    ((^a or ^b: (static member op_Implicit: ^a -> ^b) x))

let inline tryParse (text: string) : ^v option =
    let mutable output = Unchecked.defaultof< ^v>

    let parsed =
        (^v: (static member TryParse: string * byref< ^v > -> bool) (text,
                                                                      &output))

    if parsed then Some output else None

let inline bindWith (builder: ^M) (m: 'a) (f: 'b -> 'c) =
    (^M: (member Bind: 'a -> ('b -> 'c) -> 'c) (builder, m, f))

/// Everything below is lost when the parser stops at the first call.
type Gate<'T> =
    { Open: bool
      Value: 'T }

type Awaitable() =
    member _.Wait(ms: int) = Task.Delay(ms)

    member _.Name = "awaitable"

    member this.Twice(ms: int) =
        this.Wait(ms)

module Timing =
    let defaultDelay = 100

    let scale (factor: int) (ms: int) = factor * ms

    let clamp lo hi (x: int) = max lo (min hi x)

type Outcome =
    | Done
    | Failed of string

exception Timeout of string

let describe (o: Outcome) =
    match o with
    | Done -> "done"
    | Failed why -> why

let main (args: string[]) = args.Length

module Epilogue =
    let finished = true

    let report (o: Outcome) = describe o
