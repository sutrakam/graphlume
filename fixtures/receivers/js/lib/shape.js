function CjsShape(name) { // def js.shape.CjsShape
  this.name = name;
  this.describe(); // call describe -> js.shape.describe
}

CjsShape.prototype.describe = function describe() { // def js.shape.describe
  return this.label(); // call label -> js.shape.label
};

CjsShape.prototype.label = function () { return this.name; }; // def js.shape.label

module.exports = CjsShape;
