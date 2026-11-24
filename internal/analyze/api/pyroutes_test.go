package api

import (
	"slices"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// A comment inside a multi-line decorator supplied the route's path: its first
// quoted word was read as the path, so the route was reported at a path no
// one wrote.
func TestExtractFastAPICommentInsideDecorator(t *testing.T) {
	src := `router = APIRouter()

@router.get(
    # Declared before "/{order_id}" so "summary" is never read as an id (see #12).
    "/summary",
    summary="Order totals",
)
async def order_summary():
    pass
`
	routes := Extract("orders.py", []byte(src))
	assertRoute(t, routes, "GET /summary", "order_summary")
	if len(routes) != 1 {
		t.Errorf("want 1 route, got %v", signatures(routes))
	}
}

// Paths built from the module's own string constants: an f-string over a
// constant (with {{ }} escaping a path parameter), a bare constant, and a
// concatenation.
func TestExtractFastAPIPathsFromModuleConstants(t *testing.T) {
	src := `router = APIRouter(tags=["widgets"])

_BASE = "/api/widgets"
_UPLOAD: str = '/api/upload/widgets'
EXPORT_PATH = "/api/widgets/export"

@router.get(f"{_BASE}/{{widget_id}}/parts")
async def widget_parts(widget_id: int):
    pass

@router.post(
    f"{_UPLOAD}/{{widget_id}}/photos",
    status_code=201,
)
async def upload_photo(widget_id: int):
    pass

@router.get(EXPORT_PATH)
async def export_widgets():
    pass

@router.delete(_BASE + "/{widget_id}")
async def delete_widget(widget_id: int):
    pass

@router.get(f"{_BASE}/{unknown_name}/x")
async def dynamic_one():
    pass
`
	routes := Extract("widgets.py", []byte(src))
	assertRoute(t, routes, "GET /api/widgets/{widget_id}/parts", "widget_parts")
	assertRoute(t, routes, "POST /api/upload/widgets/{widget_id}/photos", "upload_photo")
	assertRoute(t, routes, "GET /api/widgets/export", "export_widgets")
	assertRoute(t, routes, "DELETE /api/widgets/{widget_id}", "delete_widget")
	// A name the module does not bind is kept as written, and the route says
	// it is assembled.
	for _, r := range routes {
		if r.Handler == "dynamic_one" {
			if !r.Dynamic || r.Confidence != model.ConfidenceMedium || r.Path != "/api/widgets/{unknown_name}/x" {
				t.Errorf("dynamic route = %+v", r)
			}
		} else if r.Dynamic {
			t.Errorf("%s marked dynamic", r.Signature())
		}
	}
	if len(routes) != 5 {
		t.Errorf("want 5 routes, got %v", signatures(routes))
	}
}

// router.add_api_route is the imperative form of a route decorator; with no
// methods= FastAPI serves GET.
func TestExtractFastAPIAddAPIRoute(t *testing.T) {
	src := `orders = APIRouter(prefix="/orders")

orders.add_api_route("/ping", ping)
orders.add_api_route(
    "/{order_id}/cancel",
    cancel_order,
    methods=["POST", "PUT"],
)
orders.add_api_route(path="/stats", endpoint=handlers.order_stats, methods=["GET"])
`
	routes := Extract("orders.py", []byte(src))
	assertRoute(t, routes, "GET /orders/ping", "ping")
	assertRoute(t, routes, "POST /orders/{order_id}/cancel", "cancel_order")
	assertRoute(t, routes, "PUT /orders/{order_id}/cancel", "cancel_order")
	assertRoute(t, routes, "GET /orders/stats", "order_stats")
	if len(routes) != 4 {
		t.Errorf("want 4 routes, got %v", signatures(routes))
	}
}

// A helper that registers one family of routes per resource, called from
// module level with literal arguments, directly or from a loop over a literal
// tuple, declares each resource's routes. A helper whose arguments are not
// literals keeps its routes as written.
func TestExtractFastAPIRouteHelperCalledWithLiterals(t *testing.T) {
	src := `router = APIRouter()

def _register(kind: str, label: str, source=None) -> None:
    async def summary(user=Depends(current_user)):
        return {}

    router.add_api_route(f"/{kind}/summary", summary, methods=["GET"])

    @router.post(f"/{kind}/rebuild")
    async def rebuild():
        return {}


_register("orders", "Orders")
_register(kind="invoices", label="Invoices")
for _k in ("refunds", 'credits'):
    _register(_k, "Money back")


def _from_registry(key):
    router.add_api_route(f"/registry/{key}/rows", rows)


for _key in registry.KEYS:
    _from_registry(_key)
`
	routes := Extract("reports.py", []byte(src))
	for _, kind := range []string{"orders", "invoices", "refunds", "credits"} {
		assertRoute(t, routes, "GET /"+kind+"/summary", "summary")
		assertRoute(t, routes, "POST /"+kind+"/rebuild", "rebuild")
	}
	assertRoute(t, routes, "GET /registry/{key}/rows", "rows")
	sigs := signatures(routes)
	if slices.Contains(sigs, "GET /{kind}/summary -> summary") {
		t.Errorf("the helper's template was reported beside its expansions: %v", sigs)
	}
	if len(routes) != 9 {
		t.Errorf("want 9 routes, got %v", sigs)
	}
}

// FastAPI's api_route decorator takes methods= as Flask's route does, and
// defaults to GET.
func TestExtractFastAPIAPIRouteDecorator(t *testing.T) {
	src := `@router.api_route("/sync", methods=["POST", "PUT"])
async def sync():
    pass

@router.api_route("/state")
async def state():
    pass
`
	routes := Extract("sync.py", []byte(src))
	assertRoute(t, routes, "POST /sync", "sync")
	assertRoute(t, routes, "PUT /sync", "sync")
	assertRoute(t, routes, "GET /state", "state")
	for _, r := range routes {
		if r.Framework != FrameworkFastAPI {
			t.Errorf("%s: framework %s", r.Signature(), r.Framework)
		}
	}
}

// The mount pass finds which router an add_api_route route is declared on by
// its line, as it does for a decorator; a commented-out mount is not a mount.
func TestPythonRoutersReadsAddAPIRouteReceivers(t *testing.T) {
	src := `widgets = APIRouter()

widgets.add_api_route("/count", count)
# app.include_router(widgets, prefix="/old")
app.include_router(widgets, prefix="/widgets")  # mounted (see #3)
`
	info := PythonRouters([]byte(src))
	if info.Receivers[3] != "widgets" {
		t.Errorf("receiver of the add_api_route line = %q (%v)", info.Receivers[3], info.Receivers)
	}
	if len(info.Mounts) != 1 || info.Mounts[0].Prefix != "/widgets" {
		t.Errorf("mounts = %+v", info.Mounts)
	}
}

func TestStripPyLineComment(t *testing.T) {
	cases := map[string]string{
		`x = "a#b"  # note`:    `x = "a#b"`,
		`@router.get("/x")  #`: `@router.get("/x")`,
		`s = 'it\'s # here'`:   `s = 'it\'s # here'`,
		`# whole line`:         ``,
		`no comment`:           `no comment`,
	}
	for in, want := range cases {
		if got := stripPyLineComment(in); got != want {
			t.Errorf("stripPyLineComment(%q) = %q, want %q", in, got, want)
		}
	}
}

// APIRouter( with its prefix= on a later line carried no prefix, so every
// route of the router lost it.
func TestExtractFastAPIMultilineRouterPrefix(t *testing.T) {
	src := `router = APIRouter(
    prefix="/internal/widgets",
    tags=["widgets"],
    dependencies=[Depends(verify_token)],
)

@router.post("/rebuild")
async def rebuild():
    pass
`
	routes := Extract("widgets_internal.py", []byte(src))
	assertRoute(t, routes, "POST /internal/widgets/rebuild", "rebuild")
	if got := PythonRouters([]byte(src)).Prefixes["router"]; got != "/internal/widgets" {
		t.Errorf("prefix = %q", got)
	}
}

// Routers mounted from a for loop over a tuple of them are each mounted.
func TestPythonRoutersReadsMountsInALoop(t *testing.T) {
	src := `from app.api import orders, invoices
from app.api.refunds import router as refunds_router

def create_app():
    app = FastAPI()
    for _upload in (
        orders.upload_router,
        invoices.upload_router,  # the second
    ):
        app.include_router(_upload, prefix="/api/upload")
    for r in [refunds_router]:
        app.include_router(r, prefix="/api/refunds")
    for x in (make_router(), other):
        app.include_router(x, prefix="/skipped")
    app.include_router(orders.router, prefix="/api/orders")
`
	info := PythonRouters([]byte(src))
	var got []string
	for _, m := range info.Mounts {
		got = append(got, m.Child+" "+m.Prefix)
	}
	want := []string{
		"orders.upload_router /api/upload",
		"invoices.upload_router /api/upload",
		"refunds_router /api/refunds",
		"x /skipped",
		"orders.router /api/orders",
	}
	if !slices.Equal(got, want) {
		t.Errorf("mounts = %q, want %q", got, want)
	}
	mod, v, ok := info.ResolveMountTarget("invoices.upload_router")
	if !ok || mod != "app.api.invoices" || v != "upload_router" {
		t.Errorf("ResolveMountTarget = %q %q %v", mod, v, ok)
	}
}
