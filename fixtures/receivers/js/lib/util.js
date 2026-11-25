'use strict';

exports.trimAll = function trimAll(s) { // def js.util.trimAll
  return s.trim(); // call trim -> none
};

exports.joinAll = (xs) => xs.join(','); // def js.util.joinAll; call join -> none

function cjsInternal() { // def js.util.cjsInternal
  return exports.trimAll('x'); // call trimAll -> js.util.trimAll
}

exports.useInternal = function useInternal() { // def js.util.useInternal
  return cjsInternal(); // call cjsInternal -> js.util.cjsInternal
};
