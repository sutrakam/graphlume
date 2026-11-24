export class ZodString {
  includes(value: string): this { // def ZodString.includes
    return this;
  }

  map(): this { // def ZodString.map
    return this;
  }

  check(): this { // def ZodString.check
    return this.map(); // call map -> ZodString.map high
  }
}
