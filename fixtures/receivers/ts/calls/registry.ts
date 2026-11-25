import { memo } from "react";

export function tsRegisterAll(): number { // def ts.registry.registerAll
  return 1;
}

export const tsMakeSchema = (n: number) => n; // def ts.registry.makeSchema

export const TS_LIMIT = 5; // def ts.registry.TS_LIMIT

export const tsWrapped = memo(function tsWrappedImpl() { // def ts.registry.wrapped
  return 1;
});

export class TsStore { // def ts.registry.TsStore
  save(): void {} // def ts.registry.save
}
