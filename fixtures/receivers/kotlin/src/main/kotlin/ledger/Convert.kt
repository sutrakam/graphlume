package ledger

// The repository declares exactly one `toLong`, an extension on its own type.
// `n.toLong()` on an Int is the standard library's, and the one declaration
// says nothing about it.
class Wide(val raw: Int) // def kt.Wide

private fun Wide.toLong(): Long { // def kt.Wide.toLong
    return raw.toLong()
}

fun total(n: Int, w: Wide): Long {
    val a = n.toLong() // call toLong -> none
    val b = (n * 2).toLong() // call toLong -> none
    return a + b + w.raw
}

class Lines {
    fun lowercaseCopy(s: String): String {
        return s.lowercase().trim() // call lowercase -> external
    }
}
