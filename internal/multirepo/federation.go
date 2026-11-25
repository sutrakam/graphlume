package multirepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

const (
	// maxServiceIDLen bounds identifiers that become row keys and report text.
	maxServiceIDLen = 128
	// maxContractsPerService bounds one sync's work and its transaction.
	maxContractsPerService = 100_000
	// maxLinksPerContract caps how many counterparts one contract may link to.
	// A route such as "GET /:x" or a topic every service touches would
	// otherwise fan out to the whole workspace; past this the remainder is
	// dropped and the service is flagged (links_truncated) so a partial graph
	// does not read as a complete one.
	maxLinksPerContract = 100
	// maxCandidatesPerContract bounds the counterpart rows read per contract.
	maxCandidatesPerContract = 1000
	// DefaultLinkLimit is the page size of Find*Links when the caller passes none.
	DefaultLinkLimit = 500
	// maxLinkLimit is the largest page a caller may ask for.
	maxLinkLimit = 5000
)

var (
	// ErrStaleSync is returned when a sync carries an older run than the one
	// already recorded for the service. Syncs are computed outside the
	// transaction, so a slow older one can reach the database after a newer one
	// and would otherwise overwrite fresh contracts with stale ones.
	ErrStaleSync = errors.New("sync is older than the state already recorded for this service")
	// ErrInvalidService and ErrInvalidContract wrap input validation failures.
	ErrInvalidService  = errors.New("invalid workspace service")
	ErrInvalidContract = errors.New("invalid exported contract")
)

var (
	validRoles      = map[string]bool{"service": true, "shared_lib": true, "gateway": true, "infra": true}
	validDirections = map[string]bool{"provides": true, "consumes": true, "reads": true, "writes": true}
	validKinds      = map[string]bool{
		"http_route": true, "grpc_method": true, "graphql_op": true, "queue_topic": true,
		"db_table": true, "cache_key": true, "config_key": true, "shared_pkg": true,
	}
)

// WorkspaceService records one registered service partition in workspace.db.
type WorkspaceService struct {
	ServiceID string
	// RelPath is the service's directory relative to the workspace root.
	RelPath string
	Role    string
	// DBPath is the absolute, clean path of the member's index.
	DBPath string
	// RunStartedUnix, CommittedRunID and MutationSeq are the member's position
	// when the sync was computed, and compared as a triple they order syncs.
	// The run's start time comes first because run ids restart at 1 when an
	// index is deleted and rebuilt (init --force): by run id alone the rebuilt
	// index would look older than the one it replaced and every later sync of
	// the service would be refused. Within one index a re-index allocates a
	// higher run id, and mutations within a run bump MutationSeq (which
	// restarts per run, so it only breaks ties under an equal run id).
	RunStartedUnix int64
	CommittedRunID int64
	MutationSeq    int64
	SchemaVersion  int
	Digest         string
	UpdatedAt      string
}

// ExportedContract defines one boundary contract declared by a service.
type ExportedContract struct {
	ID           int64
	ServiceID    string
	Direction    string // "provides", "consumes", "reads", "writes"
	ContractKind string // "http_route", "grpc_method", "graphql_op", "queue_topic", "db_table", "cache_key", "config_key", "shared_pkg"
	CanonicalKey string
	SymbolFQN    string
	FilePath     string
	Line         int
	// Confidence is one of the model.Confidence values. "exact" is accepted
	// only when Source is compiler or SCIP evidence.
	Confidence string
	// Source is the provenance of the contract: "compiler" or "scip" for
	// compiler-resolved facts, anything else (for example "heuristic",
	// "framework") for inferred ones.
	Source string
}

// ContractLink defines a resolved link between two services across a boundary.
type ContractLink struct {
	ID            int64
	SrcContractID int64
	DstContractID int64
	SrcServiceID  string
	DstServiceID  string
	LinkKind      string // "http_call", "grpc_call", "event_flow", "shared_table", "shared_cache", "shared_config", "pkg_import"
	Confidence    string
	MatchRule     string
	// Stale is set when either linked service has been re-indexed since its
	// contracts were last synced into the workspace, so the link may describe
	// a run that no longer exists.
	Stale bool
}

// LinkPage is one bounded answer from Find*Links.
type LinkPage struct {
	Links []ContractLink
	// Truncated is set when more links matched than the limit allowed.
	Truncated bool
	// FanoutTruncated is set when the queried service's links were capped at
	// sync time (see maxLinksPerContract), so the stored set is incomplete.
	FanoutTruncated bool
	// StaleServices names linked services whose current committed run differs
	// from the one recorded in the workspace.
	StaleServices []string
}

// RunLookup reports a member's current committed run, and false when it
// cannot be determined (the member is unavailable). Supplied by callers that
// hold the member pool; the manager itself never opens member indexes.
type RunLookup func(ctx context.Context, serviceID string) (runID int64, ok bool)

// PositionLookup is a [RunLookup] that also reports the run's mutation counter.
//
// WHY IT EXISTS. An incremental re-index updates the committed run in place:
// the run id stays and only mutation_seq moves. A run id alone therefore
// cannot see that kind of re-index, and links would keep reading as fresh while
// describing contracts the member no longer has.
//
// The run's start time is part of the position for the reason the sync guard
// orders by it: a rebuilt index restarts at run 1 with mutation 0, which can be
// exactly the position the workspace recorded for the index it replaced.
type PositionLookup func(ctx context.Context, serviceID string) (pos MemberPosition, ok bool)

// MemberPosition is where a member's index stands: its committed run, when that
// run started, and the run's mutation counter.
//
// Digest is the run's registry digest, empty when unknown. The start time has
// one-second resolution, so a rebuild in the same second as the recorded index
// could otherwise match it exactly; the digest is a function of the index's
// content, so differing digests mean different facts whatever the clock says.
type MemberPosition struct {
	RunStartedUnix int64
	RunID          int64
	MutationSeq    int64
	Digest         string
}

// movedFrom reports whether the member at p is no longer at the recorded
// position. Digests are compared only when both are known.
func (p MemberPosition) movedFrom(recorded MemberPosition) bool {
	if p.RunStartedUnix != recorded.RunStartedUnix || p.RunID != recorded.RunID || p.MutationSeq != recorded.MutationSeq {
		return true
	}
	return p.Digest != "" && recorded.Digest != "" && p.Digest != recorded.Digest
}

// WorkspaceManager manages reads and writes to .graphlume/workspace.db.
type WorkspaceManager struct {
	db      *store.DB
	lookupR RunLookup
	lookupP PositionLookup
}

// NewWorkspaceManager wraps an opened workspace database.
func NewWorkspaceManager(db *store.DB) *WorkspaceManager {
	return &WorkspaceManager{db: db}
}

// WithRunLookup enables staleness reporting on Find*Links.
func (w *WorkspaceManager) WithRunLookup(fn RunLookup) *WorkspaceManager {
	w.lookupR = fn
	return w
}

// WithPositionLookup enables staleness reporting that also notices an in-place
// re-index (same run id, higher mutation counter). It takes precedence over
// [WithRunLookup] when both are set.
func (w *WorkspaceManager) WithPositionLookup(fn PositionLookup) *WorkspaceManager {
	w.lookupP = fn
	return w
}

func validateService(svc WorkspaceService) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidService, fmt.Sprintf(format, a...))
	}
	if svc.ServiceID == "" || len(svc.ServiceID) > maxServiceIDLen {
		return bad("service id must be 1-%d bytes", maxServiceIDLen)
	}
	for _, r := range svc.ServiceID {
		if unicode.IsControl(r) {
			return bad("service id contains a control character")
		}
	}
	if !filepath.IsLocal(svc.RelPath) {
		return bad("rel path %q must be relative and stay inside the workspace", svc.RelPath)
	}
	if !filepath.IsAbs(svc.DBPath) || filepath.Clean(svc.DBPath) != svc.DBPath {
		return bad("db path %q must be absolute and clean", svc.DBPath)
	}
	if !validRoles[svc.Role] {
		return bad("unknown role %q", svc.Role)
	}
	return nil
}

// normalizeContract validates one contract and returns its normalised
// confidence. Heuristics never claim "exact": that value is the project's
// promise that a compiler proved the fact, so it is refused outright for any
// other source rather than quietly downgraded.
func normalizeContract(c ExportedContract) (string, error) {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidContract, fmt.Sprintf(format, a...))
	}
	if !validDirections[c.Direction] {
		return "", bad("unknown direction %q", c.Direction)
	}
	if !validKinds[c.ContractKind] {
		return "", bad("unknown contract kind %q", c.ContractKind)
	}
	if strings.TrimSpace(c.CanonicalKey) == "" || len(c.CanonicalKey) > 1024 {
		return "", bad("canonical key must be 1-1024 bytes")
	}
	conf := strings.ToLower(strings.TrimSpace(c.Confidence))
	if conf == "" {
		conf = string(model.ConfidenceMedium)
	}
	if !model.Confidence(conf).Valid() {
		return "", bad("confidence %q is not one of exact, high, medium, low", c.Confidence)
	}
	if conf == string(model.ConfidenceExact) && !isCompilerSource(c.Source) {
		return "", bad("confidence exact requires compiler or SCIP provenance, got source %q", c.Source)
	}
	return conf, nil
}

func isCompilerSource(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"compiler", "scip"} {
		if s == p || strings.HasPrefix(s, p+":") {
			return true
		}
	}
	return false
}

// matchKeyFor is the normalised key counterparts are joined on in SQL.
func matchKeyFor(kind, key string) (matchKey string, segCount int) {
	switch kind {
	case "http_route":
		_, path := splitMethodPath(key)
		return "", len(splitSegments(path))
	case "queue_topic":
		return cleanTopic(key), 0
	default:
		return strings.ToLower(strings.TrimSpace(key)), 0
	}
}

// SyncService updates the service record, refreshes its exported contracts,
// and recomputes the contract links touching this service. It returns
// ErrStaleSync, changing nothing, when the workspace already holds a newer
// state for the service.
func (w *WorkspaceManager) SyncService(ctx context.Context, svc WorkspaceService, contracts []ExportedContract) error {
	if err := validateService(svc); err != nil {
		return err
	}
	if len(contracts) > maxContractsPerService {
		return fmt.Errorf("%w: %d contracts exceeds the limit of %d", ErrInvalidContract, len(contracts), maxContractsPerService)
	}
	confs := make([]string, len(contracts))
	for i, c := range contracts {
		conf, err := normalizeContract(c)
		if err != nil {
			return fmt.Errorf("contract %d (%s): %w", i, c.CanonicalKey, err)
		}
		confs[i] = conf
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sync tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if svc.UpdatedAt == "" {
		svc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// The WHERE makes the upsert a no-op for an older state. Equal states are
	// allowed so re-running the same sync is idempotent.
	res, err := tx.ExecContext(ctx, `
INSERT INTO workspace_services (
    service_id, rel_path, role, db_path, run_started_unix, committed_run_id, mutation_seq, schema_version, digest, updated_at, links_truncated
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
ON CONFLICT(service_id) DO UPDATE SET
    rel_path = excluded.rel_path,
    role = excluded.role,
    db_path = excluded.db_path,
    run_started_unix = excluded.run_started_unix,
    committed_run_id = excluded.committed_run_id,
    mutation_seq = excluded.mutation_seq,
    schema_version = excluded.schema_version,
    digest = excluded.digest,
    updated_at = excluded.updated_at,
    links_truncated = 0
WHERE (excluded.run_started_unix, excluded.committed_run_id, excluded.mutation_seq) >=
      (workspace_services.run_started_unix, workspace_services.committed_run_id, workspace_services.mutation_seq)
`, svc.ServiceID, svc.RelPath, svc.Role, svc.DBPath, svc.RunStartedUnix, svc.CommittedRunID, svc.MutationSeq, svc.SchemaVersion, svc.Digest, svc.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert service %s: %w", svc.ServiceID, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("upsert service %s: %w", svc.ServiceID, err)
	} else if n == 0 {
		return fmt.Errorf("%w: %s at run %d/%d", ErrStaleSync, svc.ServiceID, svc.CommittedRunID, svc.MutationSeq)
	}

	// ON DELETE CASCADE removes the links of deleted contracts; the explicit
	// delete also catches links by service id in case a row predates the FKs.
	if _, err = tx.ExecContext(ctx, `DELETE FROM exported_contracts WHERE service_id = ?`, svc.ServiceID); err != nil {
		return fmt.Errorf("clear contracts for %s: %w", svc.ServiceID, err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM contract_links WHERE src_service_id = ? OR dst_service_id = ?`, svc.ServiceID, svc.ServiceID); err != nil {
		return fmt.Errorf("clear links for %s: %w", svc.ServiceID, err)
	}

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO exported_contracts (
    service_id, direction, contract_kind, canonical_key, symbol_fqn, file_path, line, confidence, source, match_key, seg_count
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return fmt.Errorf("prepare contract insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i, c := range contracts {
		mk, sc := matchKeyFor(c.ContractKind, c.CanonicalKey)
		if _, err := stmt.ExecContext(ctx, svc.ServiceID, c.Direction, c.ContractKind, c.CanonicalKey,
			c.SymbolFQN, c.FilePath, c.Line, confs[i], c.Source, mk, sc); err != nil {
			return fmt.Errorf("insert contract for %s: %w", svc.ServiceID, err)
		}
	}

	truncated, err := w.recomputeLinksInTx(ctx, tx, svc.ServiceID)
	if err != nil {
		return fmt.Errorf("recompute links: %w", err)
	}
	if truncated {
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_services SET links_truncated = 1 WHERE service_id = ?`, svc.ServiceID); err != nil {
			return fmt.Errorf("record link truncation for %s: %w", svc.ServiceID, err)
		}
	}
	return tx.Commit()
}

type contractRow struct {
	id         int64
	serviceID  string
	direction  string
	kind       string
	key        string
	confidence string
	source     string
	matchKey   string
	segCount   int
}

const contractCols = `id, service_id, direction, contract_kind, canonical_key, confidence, source, match_key, seg_count`

func scanContracts(rows *sql.Rows, limit int) ([]contractRow, error) {
	defer func() { _ = rows.Close() }()
	var out []contractRow
	for rows.Next() {
		var r contractRow
		if err := rows.Scan(&r.id, &r.serviceID, &r.direction, &r.kind, &r.key, &r.confidence, &r.source, &r.matchKey, &r.segCount); err != nil {
			return nil, err
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// recomputeLinksInTx links the service's contracts to their counterparts.
//
// It reads the service's own contracts, then for each one fetches only the
// other services' contracts that share its kind and normalised key (or, for
// routes, segment count) through the idx_ec_match index, rather than
// loading every contract in the workspace and comparing all pairs on every sync,
// which grows with the whole estate rather than with the one service changed.
// It reports whether any fan-out cap was hit.
func (w *WorkspaceManager) recomputeLinksInTx(ctx context.Context, tx *sql.Tx, serviceID string) (truncated bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+contractCols+` FROM exported_contracts WHERE service_id = ? ORDER BY id`, serviceID)
	if err != nil {
		return false, err
	}
	own, err := scanContracts(rows, 0)
	if err != nil {
		return false, err
	}

	linkStmt, err := tx.PrepareContext(ctx, `
INSERT OR IGNORE INTO contract_links (
    src_contract_id, dst_contract_id, src_service_id, dst_service_id, link_kind, confidence, match_rule
) VALUES (?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return false, err
	}
	defer func() { _ = linkStmt.Close() }()

	for _, c := range own {
		var q string
		var args []any
		if c.kind == "http_route" {
			q = `SELECT ` + contractCols + ` FROM exported_contracts
			     WHERE contract_kind = 'http_route' AND seg_count = ? AND service_id <> ? ORDER BY id LIMIT ?`
			args = []any{c.segCount, serviceID, maxCandidatesPerContract + 1}
		} else {
			q = `SELECT ` + contractCols + ` FROM exported_contracts
			     WHERE contract_kind = ? AND match_key = ? AND service_id <> ? ORDER BY id LIMIT ?`
			args = []any{c.kind, c.matchKey, serviceID, maxCandidatesPerContract + 1}
		}
		crows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return false, err
		}
		cands, err := scanContracts(crows, 0)
		if err != nil {
			return false, err
		}
		if len(cands) > maxCandidatesPerContract {
			cands = cands[:maxCandidatesPerContract]
			truncated = true
		}

		links := 0
		for _, d := range cands {
			for _, pair := range [2][2]contractRow{{c, d}, {d, c}} {
				a, b := pair[0], pair[1]
				kind, conf, rule, ok := matchContractPair(a, b)
				if !ok {
					continue
				}
				if links >= maxLinksPerContract {
					truncated = true
					break
				}
				if _, err := linkStmt.ExecContext(ctx, a.id, b.id, a.serviceID, b.serviceID, kind, conf, rule); err != nil {
					return false, err
				}
				links++
			}
			// No early exit once the cap is reached: the remaining (bounded)
			// candidates are still scanned so a further match is seen and the
			// truncation is recorded, rather than the cap landing exactly on
			// the last real link and looking complete.
		}
	}
	return truncated, nil
}

var rank = map[string]int{"exact": 4, "high": 3, "medium": 2, "low": 1}

// capConfidence lowers c to at most ceiling.
func capConfidence(c, ceiling string) string {
	if rank[c] > rank[ceiling] {
		return ceiling
	}
	return c
}

// matchContractPair determines if contract A (consumer/caller) links to
// contract B (provider/route), and how far the link may be trusted.
//
// The confidence of a link is never higher than that of either contract, and
// is then capped by how the keys were matched: a template or method-less route
// match is inference, and a shared table says only that two services touch the
// same name. "exact" survives only when both contracts are compiler/SCIP
// evidence (normalizeContract enforces that at write time and the check here
// covers rows written by older builds).
func matchContractPair(a, b contractRow) (linkKind, conf, matchRule string, ok bool) {
	base := minConfidenceStr(a.confidence, b.confidence)
	if base == "exact" && (!isCompilerSource(a.source) || !isCompilerSource(b.source)) {
		base = "high"
	}

	switch {
	// 1. HTTP calls: consumes http_route -> provides http_route
	case a.kind == "http_route" && b.kind == "http_route":
		if (a.direction == "consumes" && b.direction == "provides") || (a.direction == "writes" && b.direction == "reads") {
			if match, rule := matchHTTPKeys(a.key, b.key); match {
				if rule != "exact_route_match" {
					base = capConfidence(base, "medium")
				}
				return "http_call", base, rule, true
			}
		}

	// 2. gRPC methods: consumes grpc_method -> provides grpc_method
	case a.kind == "grpc_method" && b.kind == "grpc_method":
		if a.direction == "consumes" && b.direction == "provides" && strings.EqualFold(a.key, b.key) {
			return "grpc_call", base, "exact_proto_fqn", true
		}

	// 3. Queue / Event topic: writes queue_topic -> reads/consumes queue_topic
	case a.kind == "queue_topic" && b.kind == "queue_topic":
		if (a.direction == "writes" || a.direction == "provides") && (b.direction == "reads" || b.direction == "consumes") {
			if cleanTopic(a.key) == cleanTopic(b.key) {
				return "event_flow", base, "topic_literal", true
			}
		}

	// 4. Shared DB table. Two services naming the same table is weak evidence
	// of coupling (they may be different databases), so it is low unless one
	// writes what the other reads, which is a data-flow direction.
	case a.kind == "db_table" && b.kind == "db_table":
		if strings.EqualFold(a.key, b.key) {
			ceiling := "low"
			if (a.direction == "writes" && b.direction == "reads") || (a.direction == "reads" && b.direction == "writes") {
				ceiling = "medium"
			}
			return "shared_table", capConfidence(base, ceiling), "shared_table_name", true
		}

	// 5. Shared library / package import
	case a.kind == "shared_pkg" && b.kind == "shared_pkg":
		if a.direction == "consumes" && b.direction == "provides" && strings.EqualFold(a.key, b.key) {
			return "pkg_import", base, "package_import_path", true
		}
	}

	return "", "", "", false
}

// matchHTTPKeys checks whether an outbound call key matches a route key.
// Format: "METHOD /path/segments".
//
// A wildcard segment stands for exactly one non-empty path segment, and
// matches only where every other segment lines up literally. A call with no
// literal segment at all ("GET /:x", "POST /%s/%s") is refused: it would
// otherwise match every route of that length in the workspace. The rule names
// how it matched so the caller can cap confidence: only a literal match with a
// method on both sides is "exact_route_match".
func matchHTTPKeys(callKey, routeKey string) (bool, string) {
	methodCall, pathCall := splitMethodPath(callKey)
	methodRoute, pathRoute := splitMethodPath(routeKey)

	if methodCall != "" && methodRoute != "" && !strings.EqualFold(methodCall, methodRoute) {
		return false, ""
	}

	segsCall := splitSegments(pathCall)
	segsRoute := splitSegments(pathRoute)
	if len(segsCall) != len(segsRoute) {
		return false, ""
	}

	callLiterals := 0
	templated := false
	for i := range segsCall {
		sc, sr := segsCall[i], segsRoute[i]
		wc, wr := isParameterSegment(sc), isParameterSegment(sr)
		if !wc {
			callLiterals++
		}
		if wc || wr {
			templated = true
			// A wildcard never stands for an empty segment ("a//b").
			if (wc && sr == "") || (wr && sc == "") {
				return false, ""
			}
			continue
		}
		if sc != sr {
			return false, ""
		}
	}
	if len(segsCall) > 0 && callLiterals == 0 {
		return false, ""
	}

	switch {
	case templated:
		return true, "normalized_route_template"
	case methodCall == "" || methodRoute == "":
		return true, "route_match_no_method"
	default:
		return true, "exact_route_match"
	}
}

func splitMethodPath(sig string) (method, path string) {
	sig = strings.TrimSpace(sig)
	idx := strings.IndexByte(sig, ' ')
	if idx < 0 {
		return "", strings.TrimPrefix(sig, "/")
	}
	return strings.ToUpper(sig[:idx]), strings.TrimPrefix(strings.TrimSpace(sig[idx+1:]), "/")
}

func splitSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// Wildcard forms are recognised only as a WHOLE segment. A segment such as
// "v%d" or ":id.json" is partly literal, and treating it as a wildcard would
// let it match segments it cannot represent, so it compares as a literal.
var wildcardSegment = regexp.MustCompile(
	`^(\{[^{}/]+\}|:[A-Za-z_][A-Za-z0-9_]*|%[-+# 0-9.]*[A-Za-z]|\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^{}/]+\})$`)

func isParameterSegment(s string) bool { return wildcardSegment.MatchString(s) }

func cleanTopic(t string) string {
	t = strings.TrimPrefix(t, "kafka:")
	t = strings.TrimPrefix(t, "topic:")
	return strings.ToLower(strings.TrimSpace(t))
}

func minConfidenceStr(a, b string) string {
	rA := rank[strings.ToLower(a)]
	rB := rank[strings.ToLower(b)]
	if rA == 0 {
		rA = rank["medium"]
	}
	if rB == 0 {
		rB = rank["medium"]
	}
	if rB < rA {
		rA = rB
	}
	switch rA {
	case 4:
		return "exact"
	case 3:
		return "high"
	case 2:
		return "medium"
	default:
		return "low"
	}
}

// FindDownstreamLinks returns links from services called by the given service.
// limit <= 0 means DefaultLinkLimit.
func (w *WorkspaceManager) FindDownstreamLinks(ctx context.Context, serviceID, canonicalKey string, limit int) (LinkPage, error) {
	return w.findLinks(ctx, "src", serviceID, canonicalKey, limit)
}

// FindUpstreamLinks returns links from services that call the given service's
// endpoints. limit <= 0 means DefaultLinkLimit.
func (w *WorkspaceManager) FindUpstreamLinks(ctx context.Context, serviceID, canonicalKey string, limit int) (LinkPage, error) {
	return w.findLinks(ctx, "dst", serviceID, canonicalKey, limit)
}

func (w *WorkspaceManager) findLinks(ctx context.Context, side, serviceID, canonicalKey string, limit int) (LinkPage, error) {
	if limit <= 0 {
		limit = DefaultLinkLimit
	}
	if limit > maxLinkLimit {
		limit = maxLinkLimit
	}
	// side is one of two constants chosen by the callers above, never input.
	query := `
SELECT cl.id, cl.src_contract_id, cl.dst_contract_id, cl.src_service_id, cl.dst_service_id, cl.link_kind, cl.confidence, cl.match_rule
FROM contract_links cl
JOIN exported_contracts ec ON cl.` + side + `_contract_id = ec.id
WHERE cl.` + side + `_service_id = ?`
	args := []any{serviceID}
	if canonicalKey != "" {
		query += " AND ec.canonical_key = ?"
		args = append(args, canonicalKey)
	}
	query += " ORDER BY cl.id LIMIT ?"
	args = append(args, limit+1)

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return LinkPage{}, err
	}
	defer func() { _ = rows.Close() }()

	var page LinkPage
	for rows.Next() {
		var l ContractLink
		if err := rows.Scan(&l.ID, &l.SrcContractID, &l.DstContractID, &l.SrcServiceID, &l.DstServiceID, &l.LinkKind, &l.Confidence, &l.MatchRule); err != nil {
			return LinkPage{}, err
		}
		page.Links = append(page.Links, l)
	}
	if err := rows.Err(); err != nil {
		return LinkPage{}, err
	}
	if len(page.Links) > limit {
		page.Links = page.Links[:limit]
		page.Truncated = true
	}

	if err := w.markStale(ctx, serviceID, &page); err != nil {
		return LinkPage{}, err
	}
	return page, nil
}

// markStale compares each linked service's recorded run with its current one.
func (w *WorkspaceManager) markStale(ctx context.Context, serviceID string, page *LinkPage) error {
	var trunc int
	err := w.db.QueryRowContext(ctx, `SELECT links_truncated FROM workspace_services WHERE service_id = ?`, serviceID).Scan(&trunc)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	page.FanoutTruncated = trunc != 0

	if (w.lookupR == nil && w.lookupP == nil) || len(page.Links) == 0 {
		return nil
	}
	rows, err := w.db.QueryContext(ctx, `SELECT service_id, run_started_unix, committed_run_id, mutation_seq, digest FROM workspace_services`)
	if err != nil {
		return err
	}
	recorded := map[string]MemberPosition{}
	for rows.Next() {
		var id string
		var pos MemberPosition
		if err := rows.Scan(&id, &pos.RunStartedUnix, &pos.RunID, &pos.MutationSeq, &pos.Digest); err != nil {
			_ = rows.Close()
			return err
		}
		recorded[id] = pos
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	stale := map[string]bool{}
	check := func(id string) bool {
		if v, done := stale[id]; done {
			return v
		}
		var cur MemberPosition
		var ok bool
		if w.lookupP != nil {
			cur, ok = w.lookupP(ctx, id)
		} else {
			// A run-only lookup cannot see the counter or the start time, so
			// it must not compare them: take the recorded ones to leave only
			// the run id.
			cur = recorded[id]
			cur.RunID, ok = w.lookupR(ctx, id)
		}
		rec, known := recorded[id]
		s := ok && known && cur.movedFrom(rec)
		stale[id] = s
		return s
	}
	for i := range page.Links {
		l := &page.Links[i]
		// Evaluate both ends: || would skip the second and leave it unrecorded.
		s1, s2 := check(l.SrcServiceID), check(l.DstServiceID)
		l.Stale = s1 || s2
	}
	for id, s := range stale {
		if s {
			page.StaleServices = append(page.StaleServices, id)
		}
	}
	sort.Strings(page.StaleServices)
	return nil
}
