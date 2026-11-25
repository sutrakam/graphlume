// Fixture shader. Module-scope declarations are definitions; locals are not.

enable f16;
requires readonly_and_readwrite_storage_textures;

alias Vec3 = vec3<f32>;
alias Index = u32;

const PI = 3.14159265359;
const WORKGROUP_SIZE: u32 = 64u;
const origin = vec3<f32>(0.0, 0.0, 0.0);

override blockSize: u32 = 16;
override scale = 2.0;

struct Camera {
  view: mat4x4<f32>,
  projection: mat4x4<f32>,
  position: vec3<f32>,
}

struct Particle {
  pos: vec2<f32>,
  vel: vec2<f32>,
  @location(2) tint: vec4<f32>,
}

struct VertexOutput {
  @builtin(position) position: vec4<f32>,
  @location(0) uv: vec2<f32>,
}

@group(0) @binding(0) var<uniform> camera: Camera;
@group(0) @binding(1) var<storage, read> particlesIn: array<Particle>;
@group(0) @binding(2) var<storage, read_write> particlesOut: array<Particle>;
@group(1) @binding(0) var albedoSampler: sampler;
@group(1) @binding(1) var albedoTexture: texture_2d<f32>;

var<private> frameCounter: u32 = 0u;
var<workgroup> tile: array<f32, 64>;
var<workgroup> sharedSum: f32;

fn square(x: f32) -> f32 {
  return x * x;
}

fn lerp3(a: vec3<f32>, b: vec3<f32>, t: f32) -> vec3<f32> {
  let local = a + (b - a) * t;
  var scratch: vec3<f32> = local;
  const unused = 1.0;
  return scratch;
}

fn rotate(v: vec2<f32>,
          angle: f32) -> vec2<f32> {
  let c = cos(angle);
  let s = sin(angle);
  return vec2<f32>(c * v.x - s * v.y, s * v.x + c * v.y);
}

@vertex
fn vs_main(@builtin(vertex_index) index: u32) -> VertexOutput {
  var out: VertexOutput;
  out.position = camera.projection * camera.view * vec4<f32>(origin, 1.0);
  out.uv = vec2<f32>(f32(index), square(PI));
  return out;
}

@fragment
fn fs_main(in: VertexOutput) -> @location(0) vec4<f32> {
  return textureSample(albedoTexture, albedoSampler, in.uv);
}

@compute @workgroup_size(64)
fn simulate(@builtin(global_invocation_id) id: vec3<u32>) {
  let i = id.x;
  var p = particlesIn[i];
  p.pos = p.pos + p.vel * 0.016;
  particlesOut[i] = p;
  workgroupBarrier();
}

@compute @workgroup_size(8, 8, 1)
fn reduce(@builtin(local_invocation_index) lid: u32) {
  tile[lid] = square(f32(lid));
}

fn classify(mode: u32, uv: vec2<f32>) -> vec2<f32> {
  var result: vec2<f32>;
  switch (mode) {
    case WORKGROUP_SIZE: {
      result = uv;
      break;
    }
    default: {
      result = -uv;
      break;
    }
  }
  let afterSwitch = result * 2.0;
  const localScale = 0.5;
  var localCopy = afterSwitch * localScale;
  return localCopy;
}

fn tail_helper(k: f32) -> f32 {
  return k + PI;
}
