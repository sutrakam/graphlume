'use strict'

// A bare call cannot reach a member of an object literal. koa's response.js
// bound `vary(this.res, field)` (the npm package) to the `vary` method that
// contains it, and `new URL(...)` to a getter of another file.
const vary = require('vary')

function normalize (field) { // def normalize
  return String(field)
}

module.exports = {
  vary (field) { // def Varying.vary
    vary(this.res, normalize(field)) // call vary -> external; call normalize -> normalize high
  },

  get URL () { // def Varying.URL
    return new URL(this.href) // call URL -> external
  },

  describe () { // def Varying.describe
    return this.vary('x')
  }
}
