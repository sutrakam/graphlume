package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// Configuration and dependency reporting.
//
// Both answer a pair of questions with the same shape — what is declared, and
// what is actually used — and in both the INTERESTING answer is a mismatch:
//
//	declared but never read   a setting nobody consumes, or one read by a name
//	                          this scan cannot see
//	read but never declared   a setting the service needs and no committed file
//	                          provides, which is how a deployment fails at start
//
// Neither is reported as a verdict. Static analysis cannot see a key assembled
// at run time or a dependency loaded by reflection, so both are worded as
// prompts to check.

// ConfigBindStats reports one configuration binding pass.
type ConfigBindStats struct {
	Total   int64
	Bound   int64
	Unbound int64
	Edges   int64
}

// BindConfigUsages links configuration reads to the keys that declare them.
func (r *Registry) BindConfigUsages(ctx context.Context, runID int64) (ConfigBindStats, error) {
	var st ConfigBindStats

	keys, err := r.symbolsByName(ctx, runID, model.KindConfiguration)
	if err != nil {
		return st, err
	}
	filePaths, err := r.filePaths(ctx, runID)
	if err != nil {
		return st, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, key_name, mechanism, COALESCE(src_symbol_id, 0), start_line, file_id
		  FROM config_usages WHERE index_run_id = ?`, runID)
	if err != nil {
		return st, fmt.Errorf("registry: read config usages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		bindings []accessBinding
		edges    []model.Edge
	)
	for rows.Next() {
		var (
			id, srcID, fileID int64
			key, mechanism    string
			line              int
		)
		if err := rows.Scan(&id, &key, &mechanism, &srcID, &line, &fileID); err != nil {
			return st, err
		}
		st.Total++

		keyID, ok := matchConfigKey(keys, key)
		if !ok {
			st.Unbound++
			bindings = append(bindings, accessBinding{id: id})
			continue
		}
		st.Bound++
		bindings = append(bindings, accessBinding{id: id, boundTo: keyID})

		if srcID == 0 {
			continue
		}
		edges = append(edges, model.Edge{
			SrcID: srcID, DstID: keyID, Kind: model.EdgeConfigures,
			Confidence: model.ConfidenceHigh,
			Evidence: model.Evidence{
				FilePath: filePaths[fileID],
				Range:    model.Range{Start: model.Position{Line: line}},
				Source:   model.SourceSourceCode,
				Detail:   mechanism + " lookup",
			},
		})
	}
	if err := rows.Err(); err != nil {
		return st, err
	}

	written, err := r.AddResolvedEdges(ctx, runID, edges)
	if err != nil {
		return st, err
	}
	st.Edges = int64(written)

	if err := r.markConfigBindings(ctx, bindings); err != nil {
		return st, err
	}
	return st, nil
}

// matchConfigKey resolves a key as written at a call site to a declared key.
//
// Exact first, then case-insensitively, then across the SPELLING BOUNDARY that
// every twelve-factor service crosses: a setting declared as `database.url` in
// application.yml is read from the environment as `DATABASE_URL`. Spring, Viper
// and dotenv all define that mapping, so treating the two as different keys
// would report the same setting as both unused and missing.
func matchConfigKey(keys map[string]int64, name string) (int64, bool) {
	if id, ok := keys[name]; ok {
		return id, true
	}
	lower := strings.ToLower(name)
	if id, ok := keys[lower]; ok {
		return id, true
	}
	// DATABASE_URL -> database.url
	if dotted := strings.ReplaceAll(lower, "_", "."); dotted != lower {
		if id, ok := keys[dotted]; ok {
			return id, true
		}
	}
	// database.url -> DATABASE_URL
	if under := strings.ToUpper(strings.ReplaceAll(name, ".", "_")); under != name {
		if id, ok := keys[under]; ok {
			return id, true
		}
	}
	return 0, false
}

// symbolsByName indexes a run's symbols of one kind by name, first wins.
//
// THE ORDER BY IS LOAD-BEARING, and it was not always here. First-wins over an
// unordered read is the determinism bug this codebase has already paid for
// once: SQLite may return rows in any order it likes, so two indexes of
// identical source could pick different winners and produce different graphs.
//
// It is not a theoretical concern: configuration keys come from .env and properties
// files, where one repository rarely declares the same key twice, and from deployment
// manifests, where a Deployment, a ConfigMap and a compose file all
// naming DATABASE_URL is the ordinary case, not the exotic one, so every
// duplicate is real.
//
// The tie-break is content identity — file path, then line, then id — so the
// winner is the DECLARATION THAT COMES FIRST IN THE REPOSITORY rather than
// whichever row the storage engine handed over first.
func (r *Registry) symbolsByName(ctx context.Context, runID int64, kind model.EntityKind) (map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ?
		 ORDER BY s.name, f.path, s.start_line, s.start_col, s.id`, runID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("registry: read %s symbols: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if _, dup := out[name]; !dup {
			out[name] = id
		}
	}
	return out, rows.Err()
}

func (r *Registry) markConfigBindings(ctx context.Context, bindings []accessBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: mark config bindings: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE config_usages SET bound_symbol_id = ?, binding = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("registry: mark config bindings: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, b := range bindings {
		var target any
		status := "unbound"
		if b.boundTo > 0 {
			target = b.boundTo
			status = "bound"
		}
		if _, err := stmt.ExecContext(ctx, target, status, b.id); err != nil {
			return fmt.Errorf("registry: mark config binding %d: %w", b.id, err)
		}
	}
	return tx.Commit()
}

// ConfigKeyUsage is one configuration key and how it is used.
type ConfigKeyUsage struct {
	SymbolID int64
	Name     string
	FilePath string
	Line     int
	// Secret is true when the key's NAME indicates a credential. No value was
	// ever read to determine it.
	Secret bool
	// Readers is how many code sites read this key.
	Readers int
	// Declared is false for a key read by code that no configuration file
	// declares — the case that breaks a deployment at start-up.
	Declared bool
	// ReadBy names who reads the key when it is not application code: "graphlume"
	// for the tool's own configuration, "docker" for a Compose file. Empty for
	// an ordinary key. A key with a ReadBy must never be reported as "declared
	// and never read": no source line will ever read it, by design.
	ReadBy string `json:"read_by,omitempty"`
}

// toolReader says who reads a configuration file other than the project's own
// code. Measured on a fixture: .graphlume.yml and docker-compose.yml made every
// one of their keys read "declared and never read", which is false twice over,
// since this tool reads the first and Docker reads the second.
func toolReader(path string) string {
	base := strings.ToLower(path[strings.LastIndexAny(path, "/\\")+1:])
	switch {
	case base == ".graphlume.yml" || base == ".graphlume.yaml":
		return "graphlume"
	case strings.HasPrefix(base, "docker-compose.") || strings.HasPrefix(base, "docker-compose-") ||
		strings.HasPrefix(base, "compose.") || strings.HasPrefix(base, "compose-"):
		// docker-compose.yml, compose.yaml, compose.override.yaml; not
		// components.yml or composer.yml, which are the application's own.
		if strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml") {
			return "docker"
		}
	}
	return ""
}

// ConfigReport lists declared keys and code-read keys, merged.
func (r *Registry) ConfigReport(ctx context.Context, runID int64) ([]ConfigKeyUsage, error) {
	out := map[string]*ConfigKeyUsage{}
	// Memoised: a repository whose OpenAPI or JSON files are indexed as
	// configuration has millions of declared keys over far fewer names, and
	// the name test ran once per row (4.6 s of a 7 s report on one).
	secret := map[string]bool{}
	isSecret := func(name string) bool {
		v, ok := secret[name]
		if !ok {
			v = isSecretKeyName(name)
			secret[name] = v
		}
		return v
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, f.path, s.start_line
		  FROM symbols s JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ?
		 ORDER BY f.path, s.start_line`, runID, string(model.KindConfiguration))
	if err != nil {
		return nil, fmt.Errorf("registry: config report: %w", err)
	}
	for rows.Next() {
		var u ConfigKeyUsage
		if err := rows.Scan(&u.SymbolID, &u.Name, &u.FilePath, &u.Line); err != nil {
			_ = rows.Close()
			return nil, err
		}
		u.Declared = true
		u.ReadBy = toolReader(u.FilePath)
		u.Secret = isSecret(u.Name)
		cp := u
		out[u.Name] = &cp
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	useRows, err := r.db.QueryContext(ctx, `
		SELECT u.key_name, COUNT(*), COALESCE(u.bound_symbol_id, 0), COALESCE(MAX(f.path), ''), MAX(u.start_line)
		  FROM config_usages u JOIN files f ON f.id = u.file_id
		 WHERE u.index_run_id = ?
		 GROUP BY u.key_name, u.bound_symbol_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: config usage report: %w", err)
	}
	defer func() { _ = useRows.Close() }()

	// The entry that carries each symbol ID, for binding usages to it. This
	// was a scan of every entry per usage group; the map holds exactly the
	// entries that scan could find (one per name, the last declaration).
	byID := make(map[int64]*ConfigKeyUsage, len(out))
	for _, u := range out {
		byID[u.SymbolID] = u
	}

	for useRows.Next() {
		var (
			key     string
			count   int
			boundID int64
			path    string
			line    int
		)
		if err := useRows.Scan(&key, &count, &boundID, &path, &line); err != nil {
			return nil, err
		}
		if boundID > 0 {
			if u, ok := byID[boundID]; ok {
				u.Readers += count
			}
			continue
		}
		// Read but bound to nothing: a key no committed configuration file
		// declares. Recorded as its own entry rather than dropped — this is the
		// finding that catches a deployment about to fail at start-up.
		if existing, ok := out[key]; ok {
			existing.Readers += count
			continue
		}
		out[key] = &ConfigKeyUsage{
			Name: key, FilePath: path, Line: line, Readers: count,
			Secret: isSecret(key), Declared: false,
		}
	}
	if err := useRows.Err(); err != nil {
		return nil, err
	}

	list := make([]ConfigKeyUsage, 0, len(out))
	for _, u := range out {
		list = append(list, *u)
	}
	sortConfigKeys(list)
	return list, nil
}

// UndeclaredConfigReads is the part of ConfigReport a reader acts on first:
// the keys code reads that no indexed configuration file declares, by name, at
// most limit of them (0: all). Each row is the row ConfigReport holds for that
// key, in ConfigReport's order.
//
// It exists because ConfigReport reads every declared key to find the few that
// are not declared. On a repository whose JSON and OpenAPI files are indexed as
// configuration that is a hundred thousand rows, four seconds of a blindspots
// audit that wanted the first fifty undeclared names. A read that no
// declaration bound, of a name no declaration carries, is exactly an entry
// ConfigReport marks undeclared; a read bound to a declaration never is one.
func (r *Registry) UndeclaredConfigReads(ctx context.Context, runID int64, limit int) ([]ConfigKeyUsage, error) {
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT u.key_name, COUNT(*), COALESCE(MAX(f.path), ''), MAX(u.start_line)
		  FROM config_usages u JOIN files f ON f.id = u.file_id
		 WHERE u.index_run_id = ? AND u.bound_symbol_id IS NULL
		   AND NOT EXISTS (SELECT 1 FROM symbols s
		                    WHERE s.index_run_id = u.index_run_id
		                      AND s.name = u.key_name AND s.kind = ?)
		 GROUP BY u.key_name
		 ORDER BY u.key_name
		 LIMIT ?`, runID, string(model.KindConfiguration), limit)
	if err != nil {
		return nil, fmt.Errorf("registry: undeclared config reads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ConfigKeyUsage
	for rows.Next() {
		var u ConfigKeyUsage
		if err := rows.Scan(&u.Name, &u.Readers, &u.FilePath, &u.Line); err != nil {
			return nil, err
		}
		u.Secret = isSecretKeyName(u.Name)
		out = append(out, u)
	}
	return out, rows.Err()
}

// DependencyUsage is one declared dependency and whether anything imports it.
type DependencyUsage struct {
	SymbolID  int64
	Name      string
	Ecosystem string
	Version   string
	Scope     string
	Manifest  string
	Line      int
	// Indirect is a dependency the manifest itself marks transitive. Reporting
	// one as unused would restate a fact the manifest already makes.
	Indirect bool
	// Importers is how many indexed files import it.
	//
	// Zero means "no importer found" ONLY when a matcher was supplied. With a
	// nil ImportMatcher it means "not measured", and the two must not be
	// confused: reporting an unmeasured dependency as unused is exactly the
	// false claim this codebase exists to avoid.
	Importers int
	// ImportStatus is "imported", "not_imported" or "unknown". "unknown" is for
	// an ecosystem whose coordinates do not name what source imports (Maven
	// and Gradle): no match there shows nothing. Empty when no matcher ran.
	ImportStatus string `json:"import_status,omitempty"`
}

// ImportMatcher decides whether an import path uses a declared dependency.
//
// A PORT, injected rather than imported, and the reason is the layering rule in
// docs/development.md §1: this package is domain/core and may not depend on the analysis
// layer. Knowing that `github.com/go-git/go-git/v5/plumbing` uses the module
// `github.com/go-git/go-git/v5`, or that a Rust crate spells its hyphens as
// underscores, is ecosystem knowledge that belongs in internal/analyze/deps.
//
// The registry knows how to JOIN two tables; it does not know what a package
// manager is. Importing the matcher here compiled fine and inverted the
// dependency arrow — the kind of violation that is invisible until someone
// tries to reuse the storage layer.
type ImportMatcher func(name, ecosystem, importPath string) bool

// DependencyReport joins declared dependencies against recorded imports.
//
// The join is why this is cheap: internal/acquire already records every import
// of every file, so "is this dependency used" needs no second extraction pass.
//
// matches may be nil, in which case Importers is left at zero on every row and
// the caller must not read it as "nothing imports this" — see the field comment.
func (r *Registry) DependencyReport(ctx context.Context, runID int64, matches ImportMatcher) ([]DependencyUsage, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, COALESCE(s.signature, ''), COALESCE(s.doc, ''),
		       f.path, s.start_line, s.exported
		  FROM symbols s JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ?
		 ORDER BY f.path, s.name`, runID, string(model.KindDependency))
	if err != nil {
		return nil, fmt.Errorf("registry: dependency report: %w", err)
	}

	var list []DependencyUsage
	for rows.Next() {
		var (
			u         DependencyUsage
			signature string
			exported  int
		)
		if err := rows.Scan(&u.SymbolID, &u.Name, &signature, &u.Scope,
			&u.Manifest, &u.Line, &exported); err != nil {
			_ = rows.Close()
			return nil, err
		}
		// Signature holds "<ecosystem> <version>" — see internal/analyze.
		if i := strings.Index(signature, " "); i >= 0 {
			u.Ecosystem, u.Version = signature[:i], signature[i+1:]
		} else {
			u.Ecosystem = signature
		}
		u.Indirect = exported == 0
		list = append(list, u)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}

	if matches == nil {
		return list, nil
	}
	imports, err := r.DistinctImports(ctx, runID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		for path, count := range imports {
			if matches(list[i].Name, list[i].Ecosystem, path) {
				list[i].Importers += count
			}
		}
	}
	return list, nil
}

// DistinctImports counts how many files import each path, for one run.
//
// Exported because the join it feeds runs in both directions and only one of
// them can be answered by DependencyReport. "Which declared dependency does
// nothing import" is a question about the declared set, so it is answered here
// against the manifest rows. "Which import matches no declared dependency"
// is a question about the IMPORTED set, and the answer is a list this method's
// keys are the only source of — the caller intersects them with the declared
// set using its own ImportMatcher, because deciding whether
// `github.com/go-git/go-git/v5/plumbing` uses the module `github.com/go-git/go-git/v5`
// is ecosystem knowledge this layer must not acquire (see ImportMatcher).
//
// The count is FILES, not occurrences: two imports of the same path in one file
// are one file importing it, which is the number a reader acts on.
func (r *Registry) DistinctImports(ctx context.Context, runID int64) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT path, COUNT(DISTINCT file_id) FROM file_imports WHERE index_run_id = ? GROUP BY path`,
		runID)
	if err != nil {
		return nil, fmt.Errorf("registry: read imports: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var path string
		var n int
		if err := rows.Scan(&path, &n); err != nil {
			return nil, err
		}
		out[path] = n
	}
	return out, rows.Err()
}

// isSecretKeyName mirrors internal/analyze/config.IsSecretName.
//
// Duplicated rather than imported to keep the registry free of a dependency on
// the analysis layer, which would invert the intended direction. The rule is
// advisory — a label on a name, never a decision about a value — so a drift
// between the two costs a label, not a leak.
func isSecretKeyName(name string) bool {
	lower := strings.ToLower(name)
	// One pass over the name, testing a marker only where its first byte
	// is. It was a strings.Contains per marker: on a report of 64,000 key
	// names that was 2.9 s of a config call (the generic substring search
	// is slow on short strings, and very slow under emulation), and this is
	// 0.07 s. Same answer: a marker occurs in the name if and only if it
	// starts at some byte of it.
	for i := 0; i < len(lower); i++ {
		for _, marker := range secretMarkers {
			if marker[0] == lower[i] && strings.HasPrefix(lower[i:], marker) {
				return true
			}
		}
	}
	return false
}

// secretMarkers are the name fragments isSecretKeyName looks for.
var secretMarkers = []string{
	"password", "passwd", "secret", "token", "apikey", "api_key", "api-key",
	"private_key", "privatekey", "credential", "auth", "access_key",
	"client_secret", "encryption", "signing", "salt", "dsn", "connection_string",
}

// sortConfigKeys orders undeclared keys first, then by name. It was an
// insertion sort, which on a repository with thousands of keys spent 13 of the
// command's 23 seconds swapping whole structs; a stable sort with the same
// comparison gives the same order.
func sortConfigKeys(list []ConfigKeyUsage) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		// Undeclared keys first: a setting the code needs and nothing
		// provides is the finding worth seeing before any inventory.
		if a.Declared != b.Declared {
			return !a.Declared
		}
		return a.Name < b.Name
	})
}
