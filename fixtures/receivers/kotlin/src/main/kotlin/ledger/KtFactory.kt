package ledger

import ledger.reflect.KtFactory

// A deprecated shim in the outer package that delegates to the real class of
// the same name, which this file imports.
@Deprecated("moved")
class KtFactory : Runnable by KtFactory() { // def kt.shim.KtFactory; call KtFactory -> new kt.reflect.KtFactory
}
