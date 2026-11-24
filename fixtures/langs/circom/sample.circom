pragma circom 2.1.0;

include "circomlib/poseidon.circom";

function log2(a) {
    if (a == 0) {
        return 0;
    }
    var n = 1;
    var r = 1;
    while (n < a) {
        r++;
        n *= 2;
    }
    return r;
}

function twice(x) {
    return 2 * x;
}

template Num2Bits(n) {
    signal input in;
    signal output out[n];
    var lc1 = 0;
    var e2 = 1;
    for (var i = 0; i < n; i++) {
        out[i] <-- (in >> i) & 1;
        out[i] * (out[i] - 1) === 0;
        lc1 += out[i] * e2;
        e2 = e2 + e2;
    }
    lc1 === in;
}

template Switcher() {
    signal input sel;
    signal input L;
    signal input R;
    signal output outL;
    signal output outR;
    signal aux;
    aux <== (R - L) * sel;
    outL <== aux + L;
    outR <== -aux + R;
}

template parallel Sum(n) {
    signal input in[n];
    signal output out;
    var acc = 0;
    for (var i = 0; i < n; i++) {
        acc += in[i];
    }
    out <== acc;
}

template custom Gate(n) {
    signal input in[n];
    signal output out;
    out <== in[0];
}

template
Splitter() {
    signal input in;
    signal output lo;
    signal output hi;
    lo <== in;
    hi <== in;
}

template Wrapper() {
    signal input a;
    signal output b;
    component bits = Num2Bits(8);
    component sw = Switcher();
    component hash = Poseidon(2);
    bits.in <== a;
    sw.sel <== bits.out[0];
    sw.L <== a;
    sw.R <== twice(a);
    hash.inputs[0] <== sw.outL;
    hash.inputs[1] <== sw.outR;
    b <== hash.out;
}

template Tagged() {
    signal input {binary} flag;
    signal output {binary} res;
    res <== flag;
}

bus Point() {
    signal x;
    signal y;
}

component main {public [a]} = Wrapper();
