namespace Acme.Builders

open System
open System.Threading.Tasks

/// A generic abbreviation.
type Pending<'T> = Task<'T option>

[<Struct; NoComparison; NoEquality>]
type StateData<'T> =

    [<DefaultValue(false)>]
    val mutable Result: 'T option

    member this.IsNone =
        match this.Result with
        | None -> true
        | _ -> false

    member this.SetResult() =
        this.Result <- None

and Overall<'T> = Task<'T option>
and Resume<'T> = StateData<'T>

type BuilderBase() =

    member inline _.Delay
        (generator: unit -> Pending<'T>)
        : Pending<'T> =
        generator ()

    [<DefaultValue>]
    member inline _.Zero<'TOverall>() : Pending<unit> =
        failwith "zero"

    member inline _.Return(value: 'T) : Pending<'T> =
        failwith "return"

    member this.Plain(a: int) = a

    member inline this.WithThis(a: int) = a

    static member inline Make(x: int) = x

    member inline _.Prop = 42

type Box<'T>(item: 'T) =
    member _.Item = item

type Builder() =
    inherit BuilderBase()

    member this.Run(f) = f ()

[<AutoOpen>]
module Extensions =

    type BuilderBase with

        [<NoEagerConstraintApplication>]
        member inline _.Source(task: Task<'T>) = task

        static member inline BindDynamic(sm: byref<_>, x: int) : bool = true

module Other =
    let x = 1
