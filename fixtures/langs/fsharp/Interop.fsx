namespace Acme.Interop

open System
open System.Runtime.InteropServices
open System.Text

module Native =
    ///
    /// A bare triple slash above a declaration.
    ///
    [<DllImport("Kernel32.dll", SetLastError = true)>]
    extern uint32 QueryFullProcessImageName(nativeint hProcess, uint32 flags, StringBuilder text, [<Out>] uint32& size)

    [<DllImport("libc")>]
    extern int getpid()

    [<DllImport("libc", EntryPoint = "kill")>]
    extern int sendSignal(
        int pid,
        int signal)

    ///
    let pathOf (handle: nativeint) =
        let mutable chars = 256u
        chars

module Constraints =
    let inline awaiter (task: ^T) : ^A when ^T: (member GetAwaiter: unit -> ^A) = (^T: (member GetAwaiter: unit -> ^A) task)

    let inline both (x: ^T) : int when ^T: (member Length: int) and ^T: (member Count: int) = 1

    let after x = x

module Conditional =
    let content (md: string) : int =
        let bytes = 1
#if NET8_0_OR_GREATER
        let contentType = "a" + "; charset=utf-8"
#else
        let contentType = "text/markdown; charset=utf-8"
#endif

        fun (ctx: int) -> ctx + bytes

    let afterwards x = x

    ///
    let last x = x

module Interfaces =
    type ICache =
        abstract TryLoad: context: int -> int * int option
        abstract Save: context: int * cache: int -> unit
        abstract Clean: context: int -> unit

    let patterns x =
        let (|One|_|) = function
        | 1 -> Some()
        | _ -> None

        x

    let final x = x
