'use strict'

// `res.clone()` on a standard Response is the platform's. The repository
// declares one `clone`, on its own type, and that says nothing about it.
class Vec { // def js.Vec
  clone () { // def js.Vec.clone
    return new Vec()
  }
}

function copy (res) { // def js.copy
  return res.clone() // call clone -> none
}

module.exports = { Vec, copy }
