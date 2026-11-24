export function joinValues(values: string[]): string { // def v4.joinValues
  return values.join(" | ");
}

export const handler = {
  defineProperty(target: object, key: string): boolean { // def handler.defineProperty
    return Reflect.defineProperty(target, key, {}); // call defineProperty -> external
  },
};
