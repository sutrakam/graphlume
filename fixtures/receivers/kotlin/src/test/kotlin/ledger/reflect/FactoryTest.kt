package ledger.reflect

import ledger.Ledger

// The test tree has its own source root: the class of the same package is in
// another directory, and the shim is in the outer package.
class FactoryTest { // def kt.FactoryTest
    fun samePackage() {
        val f = KtFactory() // call KtFactory -> new kt.reflect.KtFactory
        f.make()
    }
}
