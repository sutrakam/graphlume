---------------------------- MODULE Counter ----------------------------
EXTENDS Naturals, Sequences, TLC

CONSTANT Limit, Procs
CONSTANTS Step, Start
CONSTANT Cmp(_, _)

VARIABLE count
VARIABLES log, pc

ASSUME Limit \in Nat /\ Limit > 0
ASSUME LimitPositive == Limit > 0

vars == <<count, log, pc>>

Init ==
    /\ count = Start
    /\ log = <<>>
    /\ pc = [p \in Procs |-> "idle"]

Inc(p) ==
    /\ count + Step <= Limit
    /\ count' = count + Step
    /\ log' = Append(log, p)
    /\ pc' = [pc EXCEPT ![p] = "busy"]

Reset ==
    /\ count = Limit
    /\ count' = 0
    /\ UNCHANGED <<log, pc>>

Next == \/ \E p \in Procs : Inc(p)
        \/ Reset

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

Max(a, b) == IF a > b THEN a ELSE b

Sum[s \in Seq(Nat)] ==
    IF s = <<>> THEN 0 ELSE Head(s) + Sum[Tail(s)]

Clamp(n) ==
    LET lo == 0
        hi == Limit
    IN  IF n < lo THEN lo ELSE IF n > hi THEN hi ELSE n

a \oplus b == (a + b) % 2
x (+) y == x \oplus y

LOCAL Private(n) == n + 1
-. n == 0 - n
n ^+ == n

RECURSIVE Fact(_)
Fact(n) == IF n = 0 THEN 1 ELSE n * Fact(n - 1)

Helper == INSTANCE Naturals

Alias(x) == INSTANCE Sequences WITH Seq <- Seq

TypeOK ==
    /\ count \in 0..Limit
    /\ log \in Seq(Procs)

THEOREM Safe == Spec => []TypeOK
THEOREM Spec => []LimitPositive
LEMMA Bounded == Spec => [](count <= Limit)

=========================================================================
