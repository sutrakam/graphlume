declare name "effects";
declare version "1.0";
declare options "[midi:on]";
declare delays author "fixture";

import("stdfaust.lib");
import("maths.lib");

// Multi-line definitions: the name is on the first line.
reverb(room, damp) =
  _ <: (comb1, comb2, comb3) :> _
  with {
    comb1 = fi.fb_comb(1024, 1000, 0.5, room);
    comb2 = fi.fb_comb(1024, 1117, 0.5, room);
    comb3 = fi.fb_comb(1024, 1300, damp, room);
  };

delaytime = hslider("delay [unit:ms]", 100, 1, 1000, 1)
          : si.smoo
          : *(ma.SR / 1000);

feedback = hslider("feedback", 0.3, 0, 0.95, 0.01);

echo(t, fb) = +~(de.delay(65536, t) * fb);

chorus(depth, rate) = _ <: _, de.fdelay(2048, d) :> _
with {
  d = 512 + depth * os.osc(rate);
};

// Components and library bindings.
sub = component("sub.dsp");
dsp = library("dsp.lib");
util = environment {
  half(x) = x / 2;
  quarter = 0.25;
};

// Definitions that use others are still definitions of their own.
bypass(on, fx) = _ <: select2(on, _, fx) :> _;
limiter = co.limiter_1176_R4_mono;
gainstage(g) = *(g) : fi.dcblocker;
duplicate = _ <: _, _;
mono2stereo = duplicate;
tremolo(rate, depth) = *(1 - depth * (os.osc(rate) * 0.5 + 0.5));

effect = reverb(0.8, 0.4) : echo(delaytime, feedback) : chorus(0.5, 0.2);

process = bypass(checkbox("on"), effect) <: _, _;
