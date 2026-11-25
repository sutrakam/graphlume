import { tsRegisterAll, TsStore } from "./registry";
import * as reg from "./registry";

export function tsUse(unknownThing: { save(): void }): void { // def ts.use.tsUse
  tsRegisterAll(); // call tsRegisterAll -> ts.registry.registerAll
  reg.tsMakeSchema(1); // call tsMakeSchema -> ts.registry.makeSchema
  reg.tsWrapped(); // call tsWrapped -> ts.registry.wrapped
  const store = new TsStore(); // call TsStore -> new ts.registry.TsStore
  store.save(); // call save -> ts.registry.save
  unknownThing.save(); // call save -> none
  JSON.parse("{}"); // call parse -> external
}
