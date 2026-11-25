namespace Acme.Modifiers

module internal Inner =
    let private a = 1
    let internal b x = x
    let private inline c x = x
    let inline private d x = x
    let public e = 2
    let mutable private f = 3
    let private (|Suffix|_|) (p: string) (s: string) = if s.EndsWith p then Some s else None

module private P =
    let g x = x

module public Q =
    let h x = x

[<AutoOpen>]
module internal Attributed =
    let i x = x

type internal T() =
    member internal _.M(x: int) = x
    member private _.N = 1
    static member internal S(x: int) = x
    static member private inline S2(x: int) = x
    member inline internal _.I(x: int) = x
    abstract member Ab: int -> int
    default _.Ab(x) = x

type private U = { A: int }
type public V = | X | Y
type internal W<'a> = W of 'a
exception internal Boom of string
