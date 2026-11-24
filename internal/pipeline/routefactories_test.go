package pipeline

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/api"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/missed"
	"github.com/sutrakam/graphlume/internal/registry"
)

// factoryTree is a small FastAPI service whose routes are built by a router
// factory in a shared module, registered on a router by a helper of another
// module, and registered from tables: one imported, one local, one computed.
func factoryTree() map[string]string {
	return map[string]string{
		"svc/main.py": `from fastapi import FastAPI
from svc import orders, masters, reports, taxonomy, wrapped
from svc.shared.queues import build_queue_router

app = FastAPI()
app.include_router(orders.router, prefix="/api/orders")
app.include_router(masters.router, prefix="/masters")
app.include_router(reports.router, prefix="/reports")
app.include_router(taxonomy.kinds, prefix="/kinds")
app.include_router(taxonomy.sub_kinds, prefix="/sub-kinds")
app.include_router(build_queue_router(prefix="/top"), prefix="/api")
app.include_router(wrapped.router, prefix="/wrapped")
`,
		"svc/wrapped.py": `from fastapi import APIRouter
from svc.shared.queues import build_queue_router

router = APIRouter()
router.include_router(
    with_deps(
        build_queue_router(prefix="/w"),
        extra_dependency,
    )
)
`,
		"svc/shared/__init__.py": `from .queues import build_queue_router
`,
		"svc/shared/queues.py": `from fastapi import APIRouter

# A module-level router of the same name: the factory's own is not this one.
router = APIRouter()


def build_queue_router(*, prefix: str = "", tags=None) -> APIRouter:
    router = APIRouter(prefix=prefix, tags=tags or ["queue"])

    @router.get("/queue")
    async def list_queue():
        return []

    @router.post("/queue/{item_id}/approve")
    async def approve_item(item_id: int):
        return {}

    return router


def register_health(router: APIRouter, name: str = "svc") -> None:
    @router.get(f"/health/{name}")
    async def health():
        return {}
`,
		"svc/orders.py": `from fastapi import APIRouter
from svc.shared import build_queue_router
from svc.shared.queues import register_health

router = APIRouter(prefix="/v1")
router.include_router(
    build_queue_router(
        prefix="/orders",
        tags=["orders"],
    )
)
register_health(router, name="orders")


@router.get("/{order_id}")
async def get_order(order_id: int):
    return {}
`,
		"svc/masters.py": `from svc.shared.queues import build_queue_router

router = build_queue_router()
`,
		"svc/taxonomy.py": `from fastapi import APIRouter
from svc.shared.queues import build_queue_router

kinds = APIRouter()
sub_kinds = APIRouter()

for _r, _tag in ((kinds, "kinds"), (sub_kinds, "sub-kinds")):
    _r.include_router(build_queue_router(tags=[_tag]))


@kinds.get("/")
async def list_kinds():
    return []


@sub_kinds.get("/")
async def list_sub_kinds():
    return []
`,
		"svc/datasets.py": `DATA_SETS: tuple[str, ...] = (
    "alpha",
    "beta",
)
`,
		"svc/reports.py": `from fastapi import APIRouter
from svc.datasets import DATA_SETS

router = APIRouter()


async def values():
    return {}


async def ping():
    return {}


async def echo():
    return {}


for _ds in DATA_SETS:
    router.add_api_route(f"/{_ds}/values", values, methods=["POST"])

LOCAL_ROUTES = [
    ("GET", "/ping", ping),
    ("POST", "/echo", echo),
]
for method, path, handler in LOCAL_ROUTES:
    router.add_api_route(path, handler, methods=[method])

for _k in compute_keys():
    router.add_api_route(f"/{_k}/dyn", values, methods=["GET"])
`,
	}
}

func TestPythonRouterFactoriesAndTables(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTree(t, root, factoryTree())
	reg, runID, _ := indexTree(t, root, filepath.Join(t.TempDir(), "index.db"))

	got := endpointNames(t, reg, runID)
	want := []string{
		// A factory's routes, once per call, each at the prefix its call and
		// the calling router's own mount give it.
		"GET /api/orders/v1/orders/queue",
		"POST /api/orders/v1/orders/queue/{item_id}/approve",
		"GET /masters/queue", // x = factory(); x mounted from another file
		"POST /masters/queue/{item_id}/approve",
		"GET /api/top/queue", // include_router(factory(prefix=...), prefix=...)
		"POST /api/top/queue/{item_id}/approve",
		"GET /kinds/queue", // a call in a loop over a literal table of routers
		"GET /sub-kinds/queue",
		"GET /wrapped/w/queue", // include_router(wrapper(factory(...)))
		// A helper that declares routes on the router it is passed.
		"GET /api/orders/v1/health/orders",
		"GET /api/orders/v1/{order_id}",
		// Two routers of one file, each declaring "GET /": both are served.
		"GET /kinds",
		"GET /sub-kinds",
		// Routes registered from tables: imported, and local rows.
		"POST /reports/alpha/values",
		"POST /reports/beta/values",
		"GET /reports/ping",
		"POST /reports/echo",
	}
	for _, w := range want {
		if count(got, w) != 1 {
			t.Errorf("endpoint %q found %d times, want once; got %v", w, count(got, w), got)
		}
	}
	// The factory's declaration is not also reported at a path nothing serves.
	for _, bad := range []string{"GET /queue", "GET /health/{name}", "POST /{_ds}/values"} {
		if count(got, bad) != 0 {
			t.Errorf("template %q is still reported; got %v", bad, got)
		}
	}

	// A table built at run time is not guessed at: the route keeps its path
	// as written and is reported as registered at run time.
	runtime, err := reg.RuntimeRoutes(ctx, runID, api.RuntimeRouteDoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime) != 1 || runtime[0].Name != "GET /reports/{_k}/dyn" {
		t.Errorf("runtime routes = %+v, want the one computed loop", runtime)
	}

	// blindspots reports it, with the count.
	rep, err := missed.NewDetector(reg, capability.Deps{}).Detect(ctx, runID, missed.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if rep.ByKind[missed.BlindspotRuntimeRoute] != 1 {
		t.Errorf("runtime_route blindspots = %d, want 1", rep.ByKind[missed.BlindspotRuntimeRoute])
	}
	for _, b := range rep.Blindspots {
		if b.Kind == missed.BlindspotRuntimeRoute && !strings.Contains(b.Description, "one of 1 routes registered at run time") {
			t.Errorf("description %q does not say how many", b.Description)
		}
	}

	// Derived endpoints carry the factory route's handler.
	eps, err := reg.EndpointReport(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		if e.Signature == "GET /masters/queue" || e.Signature == "GET /api/top/queue" {
			if e.Handler != "list_queue" {
				t.Errorf("%s handler = %q, want list_queue", e.Signature, e.Handler)
			}
		}
	}

	// Idempotent: a second pass derives the same endpoints and renames none.
	before := len(got)
	st, err := MountPythonRouters(ctx, reg, runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Renamed != 0 {
		t.Errorf("second pass renamed %d, want 0", st.Renamed)
	}
	if after := endpointNames(t, reg, runID); len(after) != before {
		t.Errorf("second pass changed the endpoint count from %d to %d", before, len(after))
	}
}

func endpointNames(t *testing.T, reg *registry.Registry, runID int64) []string {
	t.Helper()
	eps, err := reg.EndpointReport(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	var out []string
	for _, e := range eps {
		if seen[e.SymbolID] {
			continue
		}
		seen[e.SymbolID] = true
		out = append(out, e.Signature)
	}
	sort.Strings(out)
	return out
}

func count(list []string, s string) int {
	n := 0
	for _, x := range list {
		if x == s {
			n++
		}
	}
	return n
}
