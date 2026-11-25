package ledger

// A constructing call prefers a type nested in the enclosing type. moshi's
// LinkedHashTreeMap left KeySet() and EntrySet() unresolved and bound Node() to
// a Java test class of that name.
class Table {
    inner class KeySet { // def kt.Table.KeySet
    }

    class Node(val key: String) { // def kt.Table.Node
    }

    private val header: Node = Node("head") // call Node -> new kt.Table.Node

    fun keys(): KeySet {
        return KeySet() // call KeySet -> new kt.Table.KeySet
    }
}
