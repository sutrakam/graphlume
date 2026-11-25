var util = require('./lib/util');
var other = require('./lib/other');
var cjsFactory = require('./lib/factory');
var anonTool = require('./lib/anon-tool');
var CjsShape = require('./lib/shape');
var CjsOther = require('./lib/other-shape');
const bag = require('./lib/bag');
const { cjsAlpha, cjsBeta: renamedBeta } = require('./lib/bag');
const fs = require('fs');

function runApp(unknown) { // def js.app.runApp
  util.trimAll(' a '); // call trimAll -> js.util.trimAll
  other.trimAll(' b '); // call trimAll -> js.other.trimAll
  util.joinAll(['a']); // call joinAll -> js.util.joinAll
  cjsFactory({}); // call cjsFactory -> js.factory.cjsFactory
  anonTool(1); // call anonTool -> js.tool.anonTool
  cjsAlpha(); // call cjsAlpha -> js.bag.alpha
  bag.cjsGamma(); // call cjsGamma -> js.bag.gamma
  bag.cjsDelta(); // call cjsDelta -> js.bag.delta
  fs.readFileSync('x'); // call readFileSync -> external
  renamedBeta(); // call renamedBeta=cjsBeta -> js.bag.beta
  var shape = new CjsShape('x'); // call CjsShape -> new js.shape.CjsShape
  shape.describe(); // call describe -> js.shape.describe
  new CjsOther(); // call CjsOther -> new js.oshape.CjsOther
  unknown.describe(); // call describe -> none
  unknown.trimAll(); // call trimAll -> none
}

function useNamed(cjsProto) { // def js.app.useNamed
  cjsProto.configure(); // call configure -> js.proto.configure
  foreign.rewire(); // call rewire -> none
}
