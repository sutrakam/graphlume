'use strict';

var util = require('./util');
const { pick, omit: drop } = require('./helpers');

var app = exports = module.exports = {};
var res = Object.create(Base.prototype);
var counters = { hits: 0 };

// exports.NAME = function
exports.parse = function (input) {
  return util.trim(input);
};
exports.render = function render(view) { return view; };
module.exports.fromArrow = (x) => x * 2;
exports.Router = class Router {};
exports.alias = exports.parse;
exports.limit = 5;

// an object this file declares at its top level
app.init = function init() { return 1; };
app.handle = (req) => req;
res.send = function send(body) { return body; };
res.type = res.contentType = function contentType(type) { return type; };
counters.reset = function reset() {};
other.thing = function thing() {};
app.settings = {};

function Widget(name) { this.name = name; }
Widget.prototype.show = function show() { return this.name; };
Widget.prototype.hide = () => 0;
Widget.prototype.count = 5;

function wrapper() {
  exports.inside = function () {};
  app.late = function () {};
  this.method = function () {};
  Gadget.prototype.spin = function () {};
}
module.exports = { alpha, beta() {}, gamma: function () {}, delta: () => 1, eps: 5 };
var local = require('./util').trim;
