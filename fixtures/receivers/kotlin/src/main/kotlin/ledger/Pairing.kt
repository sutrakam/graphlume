package ledger

fun pairUp(x: Int): Int {
    val d = Duo(x, x) // call Duo -> none
    return d.a
}
