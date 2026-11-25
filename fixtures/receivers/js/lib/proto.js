var cjsProto = module.exports = {};

cjsProto.initThing = function initThing() { // def js.proto.initThing
  return this.configure(); // call configure -> js.proto.configure
};

cjsProto.configure = function configure() {}; // def js.proto.configure

foreign.rewire = function rewire() {};

function inner() {
  cjsProto.nested = function nested() {};
  return nested(); // call nested -> none
}
