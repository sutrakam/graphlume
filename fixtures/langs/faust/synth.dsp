// Fixture Faust program. Top-level `name = expr;` and `name(args) = expr;`
// are definitions; metadata, imports and definitions local to a `with` block
// are not.
declare name "synth";
declare author "fixture";

import("stdfaust.lib");

N = 32;
SR = 48000;
tau = 2 * ma.PI;
gain = hslider("gain", 0.5, 0, 1, 0.01);
freq = nentry("freq", 440, 20, 5000, 1);
volume = vslider("volume [unit:dB]", -6, -60, 0, 0.1) : ba.db2linear;
filters = library("filters.lib");
osc = environment {
  base = 220;
  tone(f) = os.osc(f);
};

square(x) = x * x;
twice(f, x) = f(f(x));
scale(k, x) = k * x;
mix(a, b) = a * 0.5 + b * 0.5;
lowpass(fc) = fi.lowpass(2, fc);

ramp(n) = +(1) ~ _ : %(n);

// A definition with `with`: the local definitions are not top-level.
voice(f) = os.sawtooth(f) * env with {
  env = en.adsr(0.01, 0.1, 0.7, 0.2, gate);
  gate = button("gate");
  shape(x) = x * x;
};

// Pattern matching: each clause is a definition of the same name.
fact(0) = 1;
fact(n) = n * fact(n-1);

// A recursion with letrec.
counter = x letrec { 'x = x + 1; };

stereo(l, r) = l, r;
pan(p) = _ <: *(1-p), *(p);

process = voice(freq) * gain * volume : lowpass(8000) <: _, _;
