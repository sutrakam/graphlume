package api

import (
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

func TestExtractPythonLiteralTableLoops(t *testing.T) {
	src := `router = APIRouter()
KINDS = ("orders", "refunds")
ROUTES = [
    ("GET", "/ping", ping),
    ("POST", "/echo", echo),
]
BY_NAME = {"alpha": "/a", "beta": "/b"}

for _k in KINDS:
    router.add_api_route(f"/{_k}/summary", summary, methods=["GET"])

for method, path, handler in ROUTES:
    router.add_api_route(path, handler, methods=[method])

for name, sub in BY_NAME.items():
    router.add_api_route(f"/by/{name}{sub}", by_name)

for _x in compute():
    router.add_api_route(f"/{_x}/dyn", dyn)
`
	routes := Extract("svc.py", []byte(src))
	assertRoute(t, routes, "GET /orders/summary", "summary")
	assertRoute(t, routes, "GET /refunds/summary", "summary")
	assertRoute(t, routes, "GET /ping", "ping")
	assertRoute(t, routes, "POST /echo", "echo")
	assertRoute(t, routes, "GET /by/alpha/a", "by_name")
	assertRoute(t, routes, "GET /by/beta/b", "by_name")
	for _, r := range routes {
		if r.Signature() == "GET {_k}/summary" || r.Signature() == "GET /{_k}/summary" {
			t.Errorf("template reported beside its rows: %v", signatures(routes))
		}
		dyn := r.Path == "/{_x}/dyn"
		if r.Runtime != dyn {
			t.Errorf("%s: Runtime = %v, want %v", r.Signature(), r.Runtime, dyn)
		}
		if dyn && r.Confidence != model.ConfidenceMedium {
			t.Errorf("%s: confidence %s, want medium", r.Signature(), r.Confidence)
		}
		if !dyn && r.Confidence != model.ConfidenceHigh {
			t.Errorf("%s: confidence %s, want high", r.Signature(), r.Confidence)
		}
	}
	if len(routes) != 7 {
		t.Errorf("want 7 routes, got %v", signatures(routes))
	}
}

func TestPyLiteralTableShapes(t *testing.T) {
	for expr, ok := range map[string]bool{
		`("a", "b")`:               true,
		`("a",)`:                   true,
		`("a")`:                    false, // a parenthesised string, not a tuple
		`["a", ("GET", "/x", h)]`:  true,
		`{"a": "/x", "b": "/y"}`:   true,
		`{"a", "b"}`:               true,
		`[k for k in KEYS]`:        false,
		`("a", load())`:            false,
		`{"a": "/x", "b"}`:         false,
		`(SOME.attr, other.thing)`: true,
	} {
		if _, got := parsePyTable(expr); got != ok {
			t.Errorf("parsePyTable(%s) ok = %v, want %v", expr, got, ok)
		}
	}
	src := []byte("X = 1\nDATA: tuple[str, ...] = (\n    \"a\",\n    \"b\",\n)\n")
	if expr, ok := PyLiteralTable(src, "DATA"); !ok || expr == "" {
		t.Errorf("PyLiteralTable(DATA) = %q, %v", expr, ok)
	}
	if _, ok := PyLiteralTable(src, "X"); ok {
		t.Error("an int is not a table")
	}
}

const factorySrc = `from fastapi import APIRouter

router = APIRouter(prefix="/module")

BASE = "/queue"


def build_queue_router(*, prefix: str = "", kind: str = "items") -> APIRouter:
    router = APIRouter(prefix=prefix)

    @router.get(BASE)
    async def list_queue():
        return []

    @router.get(f"/{kind}/latest")
    async def latest():
        return []

    return router


def register_health(router: APIRouter, name: str = "svc") -> None:
    @router.get(f"/health/{name}")
    async def health():
        return {}


@router.get("/own")
async def own():
    return {}
`

func TestPythonFactoriesAreFound(t *testing.T) {
	info := PythonRouters([]byte(factorySrc))
	byName := map[string]PyFactory{}
	for _, f := range info.Factories {
		byName[f.Name] = f
	}
	q, ok := byName["build_queue_router"]
	if !ok || !q.Returns || q.Router != "router" || q.Prefix != "prefix" {
		t.Errorf("build_queue_router = %+v", q)
	}
	h, ok := byName["register_health"]
	if !ok || h.Returns || h.Router != "router" {
		t.Errorf("register_health = %+v", h)
	}
	// The factory's local router is not the module's: its routes must not take
	// the module router's mount.
	if got := info.Receivers[11]; got != "build_queue_router().router" {
		t.Errorf("receiver at line 11 = %q", got)
	}
	if got := info.Receivers[28]; got != "router" {
		t.Errorf("receiver at line 28 = %q; %v", got, info.Receivers)
	}
}

func TestExpandPyFactory(t *testing.T) {
	routes, _, ok := ExpandPyFactory([]byte(factorySrc), "build_queue_router",
		[]string{`prefix="/orders"`, `kind=KIND`}, map[string]string{"KIND": "orders"})
	if !ok {
		t.Fatal("expansion refused")
	}
	assertRoute(t, routes, "GET /orders/queue", "list_queue")
	assertRoute(t, routes, "GET /orders/orders/latest", "latest")
	for _, r := range routes {
		if r.Confidence != model.ConfidenceHigh || r.Runtime {
			t.Errorf("%s: confidence %s runtime %v, want high and static", r.Signature(), r.Confidence, r.Runtime)
		}
	}

	// An argument that is not a literal leaves its placeholder: medium.
	routes, _, ok = ExpandPyFactory([]byte(factorySrc), "build_queue_router",
		[]string{`prefix="/x"`, `kind=pick_kind()`}, nil)
	if !ok {
		t.Fatal("expansion refused")
	}
	for _, r := range routes {
		if r.Path == "/x/{kind}/latest" && (r.Confidence != model.ConfidenceMedium || !r.Runtime) {
			t.Errorf("%s: confidence %s runtime %v, want medium, at run time", r.Signature(), r.Confidence, r.Runtime)
		}
	}

	// A prefix the call does not state: the mount is unknown, nothing is guessed.
	if _, _, ok := ExpandPyFactory([]byte(factorySrc), "build_queue_router",
		[]string{`prefix=settings.prefix`}, nil); ok {
		t.Error("a prefix read at run time was expanded")
	}

	// A factory declaring routes on the router it is passed names that router.
	routes, routerArg, ok := ExpandPyFactory([]byte(factorySrc), "register_health",
		[]string{"api", `name="orders"`}, nil)
	if !ok || routerArg != "api" {
		t.Fatalf("register_health: ok %v router %q", ok, routerArg)
	}
	assertRoute(t, routes, "GET /health/orders", "health")
}

func TestPythonFactoryCalls(t *testing.T) {
	src := `from shared import build_queue_router, register_health
import shared

ROUTERS = ((a, "a"), (b, "b"))

parent.include_router(
    build_queue_router(prefix="/orders"),
    prefix="/v1",
)
bound = shared.build_queue_router()
register_health(parent, name="svc")
for _r, _n in ROUTERS:
    _r.include_router(build_queue_router(prefix=_n))
parent.include_router(users.router, prefix="/users")
`
	info := PythonRouters([]byte(src))
	var inline, assigned, stmt, looped int
	for _, c := range info.FactoryCalls {
		switch {
		case c.Callee == "build_queue_router" && c.Parent == "parent":
			inline++
			if c.Prefix != "/v1" || len(c.Args) != 1 {
				t.Errorf("inline call = %+v", c)
			}
		case c.Bound == "bound" && c.Callee == "shared.build_queue_router":
			assigned++
		case c.Callee == "register_health":
			stmt++
		case c.Callee == "build_queue_router" && (c.Parent == "a" || c.Parent == "b"):
			looped++
		}
	}
	if inline != 1 || assigned != 1 || stmt != 1 || looped != 2 {
		t.Errorf("calls: inline %d assigned %d statement %d looped %d; %+v", inline, assigned, stmt, looped, info.FactoryCalls)
	}
	// The inline factory call is not a mount of a router named build_queue_router.
	for _, m := range info.Mounts {
		if m.Child == "build_queue_router" {
			t.Errorf("factory call read as a router mount: %+v", m)
		}
	}
}
