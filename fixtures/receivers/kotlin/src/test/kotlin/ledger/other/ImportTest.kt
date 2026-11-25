package ledger.other

import ledger.Ledger
import ledger.reflect.KtFactory

class ImportTest { // def kt.ImportTest
    fun imported() {
        val f = KtFactory() // call KtFactory -> new kt.reflect.KtFactory
        f.make()
    }
}
