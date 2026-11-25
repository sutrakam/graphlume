import * as util from "../core/util.js";

export function describe(types: string[], values: string[]): string {
  Object.defineProperty(values, "x", {}); // call defineProperty -> external
  if (types.includes("a")) { // call includes -> none
    return Array.from(values).map(String).join(","); // call map -> none
  }
  return util.joinValues(values); // call joinValues -> v4.joinValues high
}
