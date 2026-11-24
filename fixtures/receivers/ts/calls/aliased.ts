import { tsRegisterAll as tsBaseRegister, TsStore as TsBaseStore } from "./registry";
import { tsServe as tsBaseServe } from "some-package";

// A wrapper that calls the package function it is named after through an
// alias: the call is the package's, not this function's own.
export function tsServe(): number { // def ts.aliased.tsServe
  return tsBaseServe(); // call tsBaseServe=tsServe -> external
}

export function tsAliasUse(): void { // def ts.aliased.tsAliasUse
  tsBaseRegister(); // call tsBaseRegister=tsRegisterAll -> ts.registry.registerAll
  new TsBaseStore(); // call TsBaseStore=TsStore -> new ts.registry.TsStore
}
