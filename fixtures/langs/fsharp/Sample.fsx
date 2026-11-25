namespace Shop.Core

open System
open System.Collections.Generic

module Geometry =
    let pi = 3.14159
    let private secret = 42

    let area radius = pi * radius * radius

    let rec factorial n =
        if n <= 1 then 1 else n * factorial (n - 1)

    let inline square (x: int) = x * x

    let compose f g = fun x -> f (g x)

    let add = fun a b -> a + b

    let (|Even|Odd|) n = if n % 2 = 0 then Even else Odd

    let (+++) a b = a + b

    module Nested =
        let depth = 2
        let helper () =
            let inner x = x + 1
            inner depth

type Color =
    | Red
    | Green
    | Blue

type Shape =
    | Circle of radius: float
    | Rect of width: float * height: float

type Point = { X: float; Y: float }

type Id = int

type Pair<'a, 'b> = 'a * 'b

type Handler = delegate of int -> unit

type IShape =
    interface
        abstract Area: unit -> float
        abstract Name: string
    end

[<AbstractClass>]
type Base() =
    abstract Describe: unit -> string
    default this.Describe() = "base"

type Circle2(radius: float) =
    inherit Base()
    let mutable count = 0
    let secretValue = radius * 2.0
    member this.Radius = radius
    member this.Scale(factor: float) = Circle2(radius * factor)
    member val Label = "circle" with get, set
    member _.Count
        with get () = count
        and set v = count <- v
    static member Create(r: float) = Circle2(r)
    override this.Describe() = "circle"
    interface IShape with
        member this.Area() = 3.14 * radius * radius
        member this.Name = "circle"
    new() = Circle2(1.0)

type Account =
    { Balance: decimal }
    member this.Deposit(amount: decimal) = { this with Balance = this.Balance + amount }
    static member Empty = { Balance = 0m }

type Counter() =
    let mutable n = 0
    member x.Next() =
        n <- n + 1
        n

exception InsufficientFunds of decimal

let defaultColor = Color.Red

let greet (name: string) : string =
    let message = "Hello " + name
    message

let main argv =
    printfn "%d" (Geometry.factorial 5)
    0

[<RequireQualifiedAccess>]
module Util =
    let trim (s: string) = s.Trim()
    let limit = 10

type ``Quoted Name`` () =
    member this.Value = 1

type Marker =
    interface end

type Status =
    | Active = 1
    | Closed = 2

type Writer() =
    member __.Write tag = tag
    member __.WriteTwo (tag: string) value = value
    member __.Flag = true
