function helper() { return 1; }
module.exports = function createApp(options) {
  return helper(options);
};
